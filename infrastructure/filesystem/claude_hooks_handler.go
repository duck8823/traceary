package filesystem

import (
	"path/filepath"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// ClaudeHooksHandler installs Traceary hooks for the Claude Code client.
type ClaudeHooksHandler struct{}

// NewClaudeHooksHandler constructs a ClaudeHooksHandler.
func NewClaudeHooksHandler() *ClaudeHooksHandler {
	return &ClaudeHooksHandler{}
}

// Name returns the canonical client identifier.
func (h *ClaudeHooksHandler) Name() string { return "claude" }

// Build returns the Hooks aggregate Traceary installs for Claude Code.
func (h *ClaudeHooksHandler) Build(tracearyBin string) model.Hooks {
	sessionStartCommand := newHookRuntimeCommand(tracearyBin, "hook", "session", "claude", "start")
	sessionEndCommand := newHookRuntimeCommand(tracearyBin, "hook", "session", "claude", "end")
	auditCommand := newHookRuntimeCommand(tracearyBin, "hook", "audit", "claude")
	compactCommand := newHookRuntimeCommand(tracearyBin, "hook", "compact", "claude", "post-compact")
	compactResumeCommand := newHookRuntimeCommand(tracearyBin, "hook", "compact", "claude", "session-start-compact")
	promptCommand := newHookRuntimeCommand(tracearyBin, "hook", "prompt", "claude")
	transcriptCommand := newHookRuntimeCommand(tracearyBin, "hook", "transcript", "claude")

	eventOrder := []string{
		"SessionStart",
		"SessionEnd",
		"Stop",
		"PostToolUse",
		"PostToolUseFailure",
		"PostCompact",
		"UserPromptSubmit",
	}
	events := map[string][]model.HookEntry{
		"SessionStart": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-session-start", "command", sessionStartCommand, types.None[int](), "", managedKeyOf("traceary-session.sh", "claude", "start")),
			}),
			model.HookEntryOf(types.Some("compact"), []model.HookCommand{
				model.HookCommandOf("traceary-compact-session-start", "command", compactResumeCommand, types.None[int](), "", managedKeyOf("traceary-compact.sh", "claude", "session-start-compact")),
			}),
		},
		"SessionEnd": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-session-end", "command", sessionEndCommand, types.None[int](), "", managedKeyOf("traceary-session.sh", "claude", "end")),
			}),
		},
		"Stop": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-transcript", "command", transcriptCommand, types.None[int](), "", managedKeyOf("traceary-transcript.sh", "claude")),
			}),
		},
		"PostToolUse": {
			model.HookEntryOf(types.Some("Bash"), []model.HookCommand{
				model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
			model.HookEntryOf(types.Some("mcp__.*"), []model.HookCommand{
				model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
		},
		"PostToolUseFailure": {
			model.HookEntryOf(types.Some("Bash"), []model.HookCommand{
				model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
			model.HookEntryOf(types.Some("mcp__.*"), []model.HookCommand{
				model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
		},
		"PostCompact": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-compact-post-compact", "command", compactCommand, types.None[int](), "", managedKeyOf("traceary-compact.sh", "claude", "post-compact")),
			}),
		},
		"UserPromptSubmit": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-prompt", "command", promptCommand, types.None[int](), "", managedKeyOf("traceary-prompt.sh", "claude")),
			}),
		},
	}

	return model.HooksOf(eventOrder, events)
}

// DefaultInstallPath returns the standard Claude Code settings path for the
// given project directory.
func (h *ClaudeHooksHandler) DefaultInstallPath(projectDir string) (string, error) {
	return filepath.Join(projectDir, ".claude", "settings.json"), nil
}
