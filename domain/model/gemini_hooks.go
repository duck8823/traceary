package model

import (
	"github.com/duck8823/traceary/domain/types"
)

const geminiHooksDefaultTimeoutMillis = 5000

// NewGeminiHooks builds the canonical Hooks aggregate Traceary installs for
// the Gemini CLI. scriptsDir is the directory that contains the hook scripts
// and tracearyBin is the command or path used to launch the traceary binary.
func NewGeminiHooks(scriptsDir string, tracearyBin string) Hooks {
	timeout := types.Of(geminiHooksDefaultTimeoutMillis)
	sessionStartCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "gemini", "start")
	sessionEndCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "gemini", "end")
	auditCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-audit.sh", "gemini")

	eventOrder := []string{"SessionStart", "SessionEnd", "AfterTool"}
	events := map[string][]HookEntry{
		"SessionStart": {
			HookEntryOf(types.Of("*"), []HookCommand{
				HookCommandOf(
					"traceary-session-start",
					"command",
					sessionStartCommand,
					timeout,
					"Start a Traceary session",
					managedKeyOf("traceary-session.sh", "gemini", "start"),
				),
			}),
		},
		"SessionEnd": {
			HookEntryOf(types.Of("*"), []HookCommand{
				HookCommandOf(
					"traceary-session-end",
					"command",
					sessionEndCommand,
					timeout,
					"Finish a Traceary session",
					managedKeyOf("traceary-session.sh", "gemini", "end"),
				),
			}),
		},
		"AfterTool": {
			HookEntryOf(types.Of("run_shell_command"), []HookCommand{
				HookCommandOf(
					"traceary-audit",
					"command",
					auditCommand,
					timeout,
					"Record shell command audits in Traceary",
					managedKeyOf("traceary-audit.sh", "gemini"),
				),
			}),
		},
	}

	return HooksOf(eventOrder, events)
}
