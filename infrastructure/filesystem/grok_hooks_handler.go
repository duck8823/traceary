package filesystem

import (
	"path/filepath"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// GrokHooksHandler is the client-specific boundary for Grok Build hooks.
type GrokHooksHandler struct{}

// NewGrokHooksHandler constructs the Grok hook boundary.
func NewGrokHooksHandler() *GrokHooksHandler { return &GrokHooksHandler{} }

// Name returns the canonical client identifier.
func (h *GrokHooksHandler) Name() string { return "grok" }

// Build returns the native Grok hook plan for the core events verified against
// Grok Build 0.2.99. Contract events without live payload evidence are omitted.
func (h *GrokHooksHandler) Build(tracearyBin string) model.Hooks {
	const timeoutSeconds = 5
	actionByEvent := []struct {
		event  string
		action string
		name   string
	}{
		{event: "SessionStart", action: "session-start", name: "traceary-session-start"},
		{event: "UserPromptSubmit", action: "user-prompt-submit", name: "traceary-prompt"},
		{event: "PreToolUse", action: "pre-tool-use", name: "traceary-tool-pre"},
		{event: "PostToolUse", action: "post-tool-use", name: "traceary-audit"},
		{event: "Stop", action: "stop", name: "traceary-stop"},
		{event: "PreCompact", action: "pre-compact", name: "traceary-compact-pre"},
		{event: "PostCompact", action: "post-compact", name: "traceary-compact-post"},
	}

	eventOrder := make([]string, 0, len(actionByEvent))
	events := make(map[string][]model.HookEntry, len(actionByEvent))
	for _, definition := range actionByEvent {
		command := newHookRuntimeCommand(tracearyBin, "hook", "grok", definition.action)
		eventOrder = append(eventOrder, definition.event)
		events[definition.event] = []model.HookEntry{
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf(
					definition.name,
					"command",
					command,
					types.Some(timeoutSeconds),
					"",
					managedKeyOf("traceary-grok.sh", definition.action),
				),
			}),
		}
	}

	return model.HooksOf(eventOrder, events)
}

// DefaultInstallPath returns the project hook file recognized by Grok Build.
// Personal installs are routed to ~/.grok/hooks/traceary.json by the CLI's
// --global resolver.
func (h *GrokHooksHandler) DefaultInstallPath(projectDir string) (string, error) {
	return filepath.Join(projectDir, ".grok", "hooks", "traceary.json"), nil
}
