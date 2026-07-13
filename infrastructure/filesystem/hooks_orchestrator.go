package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var hooksClientAliases = map[string]string{
	"claude":          "claude",
	"claude-code":     "claude",
	"codex":           "codex",
	"codex-cli":       "codex",
	"gemini":          "gemini",
	"gemini-cli":      "gemini",
	"antigravity":     "antigravity",
	"agy":             "antigravity",
	"antigravity-cli": "antigravity",
}

// rawHookDocumentHandler is the optional interface a client handler implements
// when its hook configuration document does not fit the shared
// `{"hooks": {...}}` shape and the model.Hooks marshaller. Antigravity uses a
// top-level map of hook-group name to event configs, so its handler renders and
// merges its own document. The orchestrator dispatches to this interface before
// falling back to the model.Hooks path.
type rawHookDocumentHandler interface {
	renderDocument(tracearyBin string) ([]byte, error)
	mergeDocument(existing []byte, tracearyBin string) ([]byte, hookMergeDiff, error)
}

// HooksOrchestrator implements application.HooksOrchestrator using filesystem
// persistence and a configurable registry of client handlers.
type HooksOrchestrator struct {
	handlers map[string]application.HooksClientHandler
}

// NewHooksOrchestrator constructs a HooksOrchestrator from a map of canonical
// client name to handler implementation.
func NewHooksOrchestrator(handlers map[string]application.HooksClientHandler) *HooksOrchestrator {
	registry := make(map[string]application.HooksClientHandler, len(handlers))
	for name, handler := range handlers {
		registry[name] = handler
	}

	return &HooksOrchestrator{handlers: registry}
}

// Generate renders the hook configuration for the given client with
// the default matcher preset.
func (o *HooksOrchestrator) Generate(
	ctx context.Context,
	client string,
	tracearyBin string,
) ([]byte, error) {
	return o.GenerateWithMatcher(ctx, client, tracearyBin, "")
}

// GenerateWithMatcher is the matcher-aware variant of Generate.
func (o *HooksOrchestrator) GenerateWithMatcher(
	_ context.Context,
	client string,
	tracearyBin string,
	matcherPreset string,
) ([]byte, error) {
	handler, err := o.resolveHandler(client)
	if err != nil {
		return nil, err
	}

	if rawHandler, ok := handler.(rawHookDocumentHandler); ok {
		encoded, err := rawHandler.renderDocument(tracearyBin)
		if err != nil {
			return nil, xerrors.Errorf("failed to render hook document: %w", err)
		}
		return encoded, nil
	}

	return marshalHooks(buildHooksForInstall(handler, tracearyBin, matcherPreset))
}

// Install writes the hook configuration file for the given client
// using the client's default matcher preset.
func (o *HooksOrchestrator) Install(
	ctx context.Context,
	client string,
	tracearyBin string,
	projectDir string,
	outputPath types.Optional[string],
	force bool,
) (string, error) {
	return o.InstallWithMatcher(ctx, client, tracearyBin, projectDir, outputPath, force, "")
}

// InstallWithMatcher is the matcher-aware variant of Install (#632).
// Clients that do not honor a matcher preset ignore the value — only
// Claude Code respects it today, via ClaudeHooksHandler.BuildWithMatcher.
func (o *HooksOrchestrator) InstallWithMatcher(
	ctx context.Context,
	client string,
	tracearyBin string,
	projectDir string,
	outputPath types.Optional[string],
	force bool,
	matcherPreset string,
) (string, error) {
	path, _, err := o.installWithDiff(ctx, client, tracearyBin, projectDir, outputPath, force, matcherPreset)
	return path, err
}

// UpgradeWithMatcher applies the current hook configuration to an existing
// (or new) config file in merge-only mode and returns a diff describing
// what changed. Callers should prefer this over InstallWithMatcher when
// surfacing migration results to the user — the diff identifies which
// events were added, refreshed, or already up to date. Re-running on an
// already up-to-date config is a no-op at the byte level.
func (o *HooksOrchestrator) UpgradeWithMatcher(
	ctx context.Context,
	client string,
	tracearyBin string,
	projectDir string,
	outputPath types.Optional[string],
	matcherPreset string,
) (string, application.HookUpgradeDiff, error) {
	path, diff, err := o.installWithDiff(ctx, client, tracearyBin, projectDir, outputPath, false, matcherPreset)
	if err != nil {
		return "", application.HookUpgradeDiff{}, err
	}
	return path, application.HookUpgradeDiff{
		AddedEvents:     append([]string(nil), diff.AddedEvents...),
		RefreshedEvents: append([]string(nil), diff.RefreshedEvents...),
		PreservedEvents: append([]string(nil), diff.PreservedEvents...),
		RemovedEvents:   append([]string(nil), diff.RemovedEvents...),
	}, nil
}

// RemoveManaged removes the Traceary-owned subset of an existing hook file.
// The safe filesystem helpers reject symlink traversal on Unix, matching hook
// installation's write guarantees.
func (o *HooksOrchestrator) RemoveManaged(
	_ context.Context,
	outputPath string,
	dryRun bool,
) ([]application.HookManagedEntry, error) {
	if !filepath.IsAbs(outputPath) {
		return nil, xerrors.Errorf("hook configuration path must be absolute: %s", outputPath)
	}
	content, err := safeReadFile(outputPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to read hook configuration: %w", err)
	}
	filtered, removed, err := removeTracearyManagedHooks(content)
	if err != nil {
		return nil, err
	}
	if dryRun || len(removed) == 0 {
		return removed, nil
	}
	if err := safeWriteFileAtomic(outputPath, append(filtered, '\n'), 0o644); err != nil {
		return nil, xerrors.Errorf("failed to write hook configuration: %w", err)
	}
	return removed, nil
}

// installWithDiff is the shared implementation for InstallWithMatcher and
// UpgradeWithMatcher. The diff is empty when force overwrites the file
// (no merge performed) or when no existing file was present.
func (o *HooksOrchestrator) installWithDiff(
	_ context.Context,
	client string,
	tracearyBin string,
	projectDir string,
	outputPath types.Optional[string],
	force bool,
	matcherPreset string,
) (string, hookMergeDiff, error) {
	handler, err := o.resolveHandler(client)
	if err != nil {
		return "", hookMergeDiff{}, err
	}

	resolvedOutputPath, err := resolveInstallOutputPath(handler, projectDir, outputPath)
	if err != nil {
		return "", hookMergeDiff{}, err
	}

	encoded, diff, err := o.renderInstallContentForHandler(handler, resolvedOutputPath, tracearyBin, matcherPreset, force)
	if err != nil {
		return "", hookMergeDiff{}, err
	}

	if err := safeMkdirAll(filepath.Dir(resolvedOutputPath), 0o755); err != nil {
		return "", hookMergeDiff{}, xerrors.Errorf("failed to create output directory: %w", err)
	}
	if err := safeWriteFile(resolvedOutputPath, append(encoded, '\n'), 0o644); err != nil {
		return "", hookMergeDiff{}, xerrors.Errorf("failed to write settings file: %w", err)
	}

	return resolvedOutputPath, diff, nil
}

// matcherPresetHandler is the optional interface client handlers
// implement when they accept a matcher preset. Only Claude Code does
// today (ClaudeMatcherPreset); Codex and Gemini do not matcher-gate
// their audit hook so they fall back to the default Build(bin).
type matcherPresetHandler interface {
	BuildWithMatcher(tracearyBin string, preset ClaudeMatcherPreset) model.Hooks
}

// renderInstallContentForHandler renders the bytes to write for the given
// handler, dispatching to the raw-document path for handlers whose document
// shape is incompatible with the shared model.Hooks marshaller (Antigravity).
func (o *HooksOrchestrator) renderInstallContentForHandler(
	handler application.HooksClientHandler,
	resolvedOutputPath string,
	tracearyBin string,
	matcherPreset string,
	force bool,
) ([]byte, hookMergeDiff, error) {
	rawHandler, ok := handler.(rawHookDocumentHandler)
	if !ok {
		hooks := buildHooksForInstall(handler, tracearyBin, matcherPreset)
		return renderInstallContent(resolvedOutputPath, hooks, force)
	}

	if force {
		encoded, err := rawHandler.renderDocument(tracearyBin)
		if err != nil {
			return nil, hookMergeDiff{}, xerrors.Errorf("failed to render hook document: %w", err)
		}
		return encoded, hookMergeDiff{}, nil
	}

	existingContent, err := safeReadFile(resolvedOutputPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			encoded, diff, mergeErr := rawHandler.mergeDocument(nil, tracearyBin)
			if mergeErr != nil {
				return nil, hookMergeDiff{}, xerrors.Errorf("failed to render hook document: %w", mergeErr)
			}
			return encoded, diff, nil
		}
		return nil, hookMergeDiff{}, xerrors.Errorf("failed to read existing hooks file: %w", err)
	}
	encoded, diff, mergeErr := rawHandler.mergeDocument(existingContent, tracearyBin)
	if mergeErr != nil {
		return nil, hookMergeDiff{}, xerrors.Errorf("failed to merge hook document: %w", mergeErr)
	}
	return encoded, diff, nil
}

func buildHooksForInstall(handler application.HooksClientHandler, tracearyBin string, matcherPreset string) model.Hooks {
	if matcherPreset == "" {
		return handler.Build(tracearyBin)
	}
	preset := ClaudeMatcherPreset(matcherPreset)
	if mph, ok := handler.(matcherPresetHandler); ok && preset.IsValid() {
		return mph.BuildWithMatcher(tracearyBin, preset)
	}
	return handler.Build(tracearyBin)
}

// SupportedClients returns the canonical client identifiers registered with
// the orchestrator in stable (alphabetical) order.
func (o *HooksOrchestrator) SupportedClients() []string {
	names := make([]string, 0, len(o.handlers))
	for name := range o.handlers {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

// ResolveInstallPath returns the resolved install path for a client given a
// project directory and an optional override. Exposed for doctor-style
// inspection checks that need to know where a client expects its config
// without rendering any file.
func (o *HooksOrchestrator) ResolveInstallPath(
	client string,
	projectDir string,
	outputPath types.Optional[string],
) (string, error) {
	handler, err := o.resolveHandler(client)
	if err != nil {
		return "", err
	}

	return resolveInstallOutputPath(handler, projectDir, outputPath)
}

// NormalizeClient returns the canonical name for a client alias. It returns
// an error when the alias is not registered.
func (o *HooksOrchestrator) NormalizeClient(client string) (string, error) {
	return normalizeHooksClient(o.handlers, client)
}

func (o *HooksOrchestrator) resolveHandler(client string) (application.HooksClientHandler, error) {
	resolvedClient, err := normalizeHooksClient(o.handlers, client)
	if err != nil {
		return nil, err
	}

	handler, ok := o.handlers[resolvedClient]
	if !ok {
		return nil, xerrors.Errorf("client handler is not registered: %s", resolvedClient)
	}

	return handler, nil
}

func normalizeHooksClient(handlers map[string]application.HooksClientHandler, client string) (string, error) {
	trimmedClient := strings.ToLower(strings.TrimSpace(client))
	if resolved, ok := hooksClientAliases[trimmedClient]; ok {
		if _, registered := handlers[resolved]; registered {
			return resolved, nil
		}
	}

	return "", xerrors.Errorf(
		"unsupported client: %s (valid values: claude, codex, gemini, antigravity; aliases: claude-code, codex-cli, gemini-cli, agy, antigravity-cli)",
		client,
	)
}

func resolveInstallOutputPath(
	handler application.HooksClientHandler,
	projectDir string,
	outputPath types.Optional[string],
) (string, error) {
	if value, ok := outputPath.Value(); ok {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			resolvedPath, err := filepath.Abs(trimmedValue)
			if err != nil {
				return "", xerrors.Errorf("failed to resolve absolute path: %w", err)
			}

			return resolvedPath, nil
		}
	}

	defaultPath, err := handler.DefaultInstallPath(projectDir)
	if err != nil {
		return "", xerrors.Errorf("failed to resolve default install path: %w", err)
	}

	return defaultPath, nil
}

func renderInstallContent(resolvedOutputPath string, hooks model.Hooks, force bool) ([]byte, hookMergeDiff, error) {
	var diff hookMergeDiff
	if force {
		encoded, err := marshalHooks(hooks)
		return encoded, diff, err
	}

	existingContent, err := safeReadFile(resolvedOutputPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			encoded, createDiff, merr := mergeHooksDocumentWithDiff(nil, hooks)
			if merr != nil {
				return nil, hookMergeDiff{}, xerrors.Errorf("failed to render new hook configuration: %w", merr)
			}
			return encoded, createDiff, nil
		}

		return nil, diff, xerrors.Errorf("failed to read existing settings file: %w", err)
	}

	mergedContent, mergeDiff, err := mergeHooksDocumentWithDiff(existingContent, hooks)
	if err != nil {
		return nil, hookMergeDiff{}, xerrors.Errorf("failed to merge existing hook configuration: %w", err)
	}

	return mergedContent, mergeDiff, nil
}
