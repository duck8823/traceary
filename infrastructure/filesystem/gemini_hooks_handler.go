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
func (h *GeminiHooksHandler) Build(tracearyBin string) model.Hooks {
	timeout := types.Some(geminiHooksDefaultTimeoutMillis)
	sessionStartCommand := newHookRuntimeCommand(tracearyBin, "hook", "session", "gemini", "start")
	sessionEndCommand := newHookRuntimeCommand(tracearyBin, "hook", "session", "gemini", "end")
	auditCommand := newHookRuntimeCommand(tracearyBin, "hook", "audit", "gemini")
	transcriptCommand := newHookRuntimeCommand(tracearyBin, "hook", "transcript", "gemini")

	// Gemini has no Stop event — the closest analogue is AfterAgent,
	// which fires once the agent has produced a complete response and
	// includes the response text inline as `prompt_response`. We wire
	// transcript capture there so parity with Claude Code / Codex is
	// preserved without needing a Stop-equivalent from the host.
	eventOrder := []string{"SessionStart", "SessionEnd", "AfterAgent", "AfterTool"}
	events := map[string][]model.HookEntry{
		"SessionStart": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
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
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
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
		"AfterAgent": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf(
					"traceary-transcript",
					"command",
					transcriptCommand,
					timeout,
					"Record agent response as a transcript event",
					managedKeyOf("traceary-transcript.sh", "gemini"),
				),
			}),
		},
		"AfterTool": {
			model.HookEntryOf(types.Some("run_shell_command"), []model.HookCommand{
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
