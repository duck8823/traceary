package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var hooksClientAliases = map[string]string{
	"claude":      "claude",
	"claude-code": "claude",
	"codex":       "codex",
	"codex-cli":   "codex",
	"gemini":      "gemini",
	"gemini-cli":  "gemini",
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

// Generate renders the hook configuration for the given client.
func (o *HooksOrchestrator) Generate(
	_ context.Context,
	client string,
	scriptsDir string,
	tracearyBin string,
) ([]byte, error) {
	handler, err := o.resolveHandler(client)
	if err != nil {
		return nil, err
	}

	return marshalHooks(handler.Build(scriptsDir, tracearyBin))
}

// Install writes the hook configuration file for the given client.
func (o *HooksOrchestrator) Install(
	_ context.Context,
	client string,
	scriptsDir string,
	tracearyBin string,
	projectDir string,
	outputPath types.Optional[string],
	force bool,
) (string, error) {
	handler, err := o.resolveHandler(client)
	if err != nil {
		return "", err
	}

	resolvedOutputPath, err := resolveInstallOutputPath(handler, projectDir, outputPath)
	if err != nil {
		return "", err
	}

	hooks := handler.Build(scriptsDir, tracearyBin)
	encoded, err := renderInstallContent(resolvedOutputPath, hooks, force)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(resolvedOutputPath), 0o755); err != nil {
		return "", xerrors.Errorf("failed to create output directory: %w", err)
	}
	if err := os.WriteFile(resolvedOutputPath, append(encoded, '\n'), 0o644); err != nil {
		return "", xerrors.Errorf("failed to write settings file: %w", err)
	}

	return resolvedOutputPath, nil
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
		"unsupported client: %s (valid values: claude, codex, gemini; aliases: claude-code, codex-cli, gemini-cli)",
		client,
	)
}

func resolveInstallOutputPath(
	handler application.HooksClientHandler,
	projectDir string,
	outputPath types.Optional[string],
) (string, error) {
	if value, ok := outputPath.Get(); ok {
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

func renderInstallContent(resolvedOutputPath string, hooks model.Hooks, force bool) ([]byte, error) {
	if _, err := os.Stat(resolvedOutputPath); err != nil {
		if os.IsNotExist(err) {
			return marshalHooks(hooks)
		}

		return nil, xerrors.Errorf("failed to inspect existing file: %w", err)
	}

	if force {
		return marshalHooks(hooks)
	}

	existingContent, err := os.ReadFile(resolvedOutputPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to read existing settings file: %w", err)
	}

	mergedContent, err := mergeHooksDocument(existingContent, hooks)
	if err != nil {
		return nil, xerrors.Errorf("failed to merge existing hook configuration: %w", err)
	}

	return mergedContent, nil
}
