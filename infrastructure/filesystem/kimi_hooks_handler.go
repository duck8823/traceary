package filesystem

import (
	"fmt"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// kimiHookTimeoutSeconds is the per-hook timeout written into the generated
// Kimi Code [[hooks]] rules. Kimi's default is 30s; Traceary's runtime
// entrypoints are fail-soft and quick, so 5s keeps a slow disk from stalling
// the host loop while leaving ample headroom (matching the Grok contract).
const kimiHookTimeoutSeconds = 5

// KimiHooksHandler is the client-specific boundary for Kimi Code hooks.
//
// Kimi Code reads hooks from [[hooks]] rules in ~/.kimi-code/config.toml
// (TOML), which is incompatible with the shared {"hooks": {...}} JSON
// document, so this handler renders its own TOML document via the
// rawHookDocumentHandler interface for `hooks print`. Installation stays
// fail-closed: the Traceary Kimi plugin (kimi.plugin.json, which declares
// hooks in JSON) is the distribution path, and `hooks install` does not
// merge TOML into the user's config.toml.
type KimiHooksHandler struct{}

// NewKimiHooksHandler constructs the Kimi hook boundary.
func NewKimiHooksHandler() *KimiHooksHandler { return &KimiHooksHandler{} }

// Name returns the canonical client identifier.
func (h *KimiHooksHandler) Name() string { return "kimi" }

// Build satisfies application.HooksClientHandler. Kimi's document is rendered
// by renderDocument (TOML is incompatible with the shared model.Hooks
// marshaller), so this returns an empty aggregate and is never used for Kimi
// generation.
func (h *KimiHooksHandler) Build(_ string) model.Hooks {
	return model.HooksOf(nil, nil)
}

// DefaultInstallPath fails closed: Traceary does not merge hooks into the
// user's config.toml. The Traceary Kimi plugin is the distribution path.
func (h *KimiHooksHandler) DefaultInstallPath(_ string) (string, error) {
	return "", kimiInstallUnavailableError()
}

// validateInstall keeps every install path (default, --output, --force,
// --upgrade) fail-closed until TOML merge support is a deliberate feature.
func (h *KimiHooksHandler) validateInstall() error {
	return kimiInstallUnavailableError()
}

func kimiInstallUnavailableError() error {
	return xerrors.Errorf("kimi hook installation is not available: the Traceary Kimi plugin (kimi.plugin.json) is the distribution path, or append `traceary hooks print --client kimi` output to ~/.kimi-code/config.toml manually")
}

// kimiHookRule is one [[hooks]] rule in the rendered TOML document.
type kimiHookRule struct {
	event   string
	action  string
	comment string
}

// kimiHookPlan lists the Kimi Code events wired to Traceary runtime
// entrypoints, limited to the events with live payload evidence in the host
// contract (docs/hooks/host-contract.json). PreToolUse is a validation-only
// boundary because PostToolUse already carries both input and output.
var kimiHookPlan = []kimiHookRule{
	{event: "SessionStart", action: "session-start", comment: "session boundary (start)"},
	{event: "SessionEnd", action: "session-end", comment: "session boundary (end)"},
	{event: "UserPromptSubmit", action: "user-prompt-submit", comment: "prompt"},
	{event: "PreToolUse", action: "pre-tool-use", comment: "validation-only boundary (no duplicate audit)"},
	{event: "PostToolUse", action: "post-tool-use", comment: "tool audit"},
	{event: "PostToolUseFailure", action: "post-tool-use-failure", comment: "tool audit (failure)"},
	{event: "Stop", action: "stop", comment: "assistant transcript (best-effort wire log side channel)"},
}

// renderDocument renders a fresh TOML document of [[hooks]] rules containing
// only the Traceary entries, suitable for appending to
// ~/.kimi-code/config.toml.
func (h *KimiHooksHandler) renderDocument(tracearyBin string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("# Traceary session and shell-audit hooks for Kimi Code.\n")
	b.WriteString("# Append to ~/.kimi-code/config.toml (or install the Traceary Kimi plugin,\n")
	b.WriteString("# which declares the same hooks in its kimi.plugin.json manifest).\n")
	for _, rule := range kimiHookPlan {
		command := newHookRuntimeCommand(tracearyBin, "hook", "kimi", rule.action)
		fmt.Fprintf(&b, "\n# %s\n[[hooks]]\nevent = %q\ncommand = %q\ntimeout = %d\n",
			rule.comment, rule.event, command, kimiHookTimeoutSeconds)
	}
	return []byte(b.String()), nil
}

// mergeDocument is unreachable: validateInstall fails closed before the
// orchestrator attempts a merge. It exists to satisfy the
// rawHookDocumentHandler interface contract.
func (h *KimiHooksHandler) mergeDocument(_ []byte, _ string) ([]byte, hookMergeDiff, error) {
	return nil, hookMergeDiff{}, kimiInstallUnavailableError()
}
