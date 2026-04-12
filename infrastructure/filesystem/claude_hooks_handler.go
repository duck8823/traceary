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
// scriptsDir is the directory that contains the hook scripts and
// tracearyBin is the command or path used to launch the traceary binary.
func (h *ClaudeHooksHandler) Build(scriptsDir string, tracearyBin string) model.Hooks {
	sessionStartCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "claude", "start")
	sessionEndCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "claude", "end")
	auditCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-audit.sh", "claude")
	compactCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-compact.sh", "claude", "post-compact")
	compactResumeCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-compact.sh", "claude", "session-start-compact")
	promptCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-prompt.sh", "claude")

	eventOrder := []string{
		"SessionStart",
		"SessionEnd",
		"PostToolUse",
		"PostToolUseFailure",
		"PostCompact",
		"UserPromptSubmit",
	}
	events := map[string][]model.HookEntry{
		"SessionStart": {
			model.HookEntryOf(types.Of("*"), []model.HookCommand{
				model.HookCommandOf("", "command", sessionStartCommand, types.Empty[int](), "", managedKeyOf("traceary-session.sh", "claude", "start")),
			}),
			model.HookEntryOf(types.Of("compact"), []model.HookCommand{
				model.HookCommandOf("", "command", compactResumeCommand, types.Empty[int](), "", managedKeyOf("traceary-compact.sh", "claude", "session-start-compact")),
			}),
		},
		"SessionEnd": {
			model.HookEntryOf(types.Of("*"), []model.HookCommand{
				model.HookCommandOf("", "command", sessionEndCommand, types.Empty[int](), "", managedKeyOf("traceary-session.sh", "claude", "end")),
			}),
		},
		"PostToolUse": {
			model.HookEntryOf(types.Of("Bash"), []model.HookCommand{
				model.HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
			model.HookEntryOf(types.Of("mcp__.*"), []model.HookCommand{
				model.HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
		},
		"PostToolUseFailure": {
			model.HookEntryOf(types.Of("Bash"), []model.HookCommand{
				model.HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
			model.HookEntryOf(types.Of("mcp__.*"), []model.HookCommand{
				model.HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
		},
		"PostCompact": {
			model.HookEntryOf(types.Of("*"), []model.HookCommand{
				model.HookCommandOf("", "command", compactCommand, types.Empty[int](), "", managedKeyOf("traceary-compact.sh", "claude", "post-compact")),
			}),
		},
		"UserPromptSubmit": {
			model.HookEntryOf(types.Of("*"), []model.HookCommand{
				model.HookCommandOf("", "command", promptCommand, types.Empty[int](), "", managedKeyOf("traceary-prompt.sh", "claude")),
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
