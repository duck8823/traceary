package model

import (
	"github.com/duck8823/traceary/domain/types"
)

// NewCodexHooks builds the canonical Hooks aggregate Traceary installs for
// the Codex CLI. scriptsDir is the directory that contains the hook scripts
// and tracearyBin is the command or path used to launch the traceary binary.
func NewCodexHooks(scriptsDir string, tracearyBin string) Hooks {
	sessionStartCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "codex", "start")
	sessionStopCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "codex", "stop")
	auditCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-audit.sh", "codex")

	eventOrder := []string{"SessionStart", "Stop", "PostToolUse"}
	events := map[string][]HookEntry{
		"SessionStart": {
			HookEntryOf(types.Empty[string](), []HookCommand{
				HookCommandOf("", "command", sessionStartCommand, types.Empty[int](), "", managedKeyOf("traceary-session.sh", "codex", "start")),
			}),
		},
		"Stop": {
			HookEntryOf(types.Empty[string](), []HookCommand{
				HookCommandOf("", "command", sessionStopCommand, types.Empty[int](), "", managedKeyOf("traceary-session.sh", "codex", "stop")),
			}),
		},
		"PostToolUse": {
			HookEntryOf(types.Of(""), []HookCommand{
				HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "codex")),
			}),
		},
	}

	return HooksOf(eventOrder, events)
}
