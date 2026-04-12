package model

import (
	"github.com/duck8823/traceary/domain/types"
)

// NewClaudeHooks builds the canonical Hooks aggregate Traceary installs for
// Claude Code. scriptsDir is the directory that contains the hook scripts and
// tracearyBin is the command or path used to launch the traceary binary.
func NewClaudeHooks(scriptsDir string, tracearyBin string) Hooks {
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
	events := map[string][]HookEntry{
		"SessionStart": {
			HookEntryOf(types.Of("*"), []HookCommand{
				HookCommandOf("", "command", sessionStartCommand, types.Empty[int](), "", managedKeyOf("traceary-session.sh", "claude", "start")),
			}),
			HookEntryOf(types.Of("compact"), []HookCommand{
				HookCommandOf("", "command", compactResumeCommand, types.Empty[int](), "", managedKeyOf("traceary-compact.sh", "claude", "session-start-compact")),
			}),
		},
		"SessionEnd": {
			HookEntryOf(types.Of("*"), []HookCommand{
				HookCommandOf("", "command", sessionEndCommand, types.Empty[int](), "", managedKeyOf("traceary-session.sh", "claude", "end")),
			}),
		},
		"PostToolUse": {
			HookEntryOf(types.Of("Bash"), []HookCommand{
				HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
			HookEntryOf(types.Of("mcp__.*"), []HookCommand{
				HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
		},
		"PostToolUseFailure": {
			HookEntryOf(types.Of("Bash"), []HookCommand{
				HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
			HookEntryOf(types.Of("mcp__.*"), []HookCommand{
				HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
			}),
		},
		"PostCompact": {
			HookEntryOf(types.Of("*"), []HookCommand{
				HookCommandOf("", "command", compactCommand, types.Empty[int](), "", managedKeyOf("traceary-compact.sh", "claude", "post-compact")),
			}),
		},
		"UserPromptSubmit": {
			HookEntryOf(types.Of("*"), []HookCommand{
				HookCommandOf("", "command", promptCommand, types.Empty[int](), "", managedKeyOf("traceary-prompt.sh", "claude")),
			}),
		},
	}

	return HooksOf(eventOrder, events)
}
