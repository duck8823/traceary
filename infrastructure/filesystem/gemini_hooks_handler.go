package filesystem

import (
	"path/filepath"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const geminiHooksDefaultTimeoutMillis = 5000

// GeminiHooksHandler installs Traceary hooks for the Gemini CLI.
type GeminiHooksHandler struct{}

// NewGeminiHooksHandler constructs a GeminiHooksHandler.
func NewGeminiHooksHandler() *GeminiHooksHandler {
	return &GeminiHooksHandler{}
}

// Name returns the canonical client identifier.
func (h *GeminiHooksHandler) Name() string { return "gemini" }

// Build returns the Hooks aggregate Traceary installs for Gemini CLI.
// scriptsDir is the directory that contains the hook scripts and
// tracearyBin is the command or path used to launch the traceary binary.
func (h *GeminiHooksHandler) Build(scriptsDir string, tracearyBin string) model.Hooks {
	timeout := types.Of(geminiHooksDefaultTimeoutMillis)
	sessionStartCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "gemini", "start")
	sessionEndCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "gemini", "end")
	auditCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-audit.sh", "gemini")

	eventOrder := []string{"SessionStart", "SessionEnd", "AfterTool"}
	events := map[string][]model.HookEntry{
		"SessionStart": {
			model.HookEntryOf(types.Of("*"), []model.HookCommand{
				model.HookCommandOf(
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
			model.HookEntryOf(types.Of("*"), []model.HookCommand{
				model.HookCommandOf(
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
			model.HookEntryOf(types.Of("run_shell_command"), []model.HookCommand{
				model.HookCommandOf(
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

	return model.HooksOf(eventOrder, events)
}

// DefaultInstallPath returns the standard Gemini settings path for the given
// project directory.
func (h *GeminiHooksHandler) DefaultInstallPath(projectDir string) (string, error) {
	return filepath.Join(projectDir, ".gemini", "settings.json"), nil
}
