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
	promptCommand := newHookRuntimeCommand(tracearyBin, "hook", "prompt", "gemini")
	preCompressCommand := newHookRuntimeCommand(tracearyBin, "hook", "compact", "gemini", "pre-compact")

	// Gemini has no Stop event — the closest analogue is AfterAgent,
	// which fires once the agent has produced a complete response and
	// includes the response text inline as `prompt_response`. We wire
	// transcript capture there so parity with Claude Code / Codex is
	// preserved without needing a Stop-equivalent from the host.
	//
	// BeforeAgent fires when the user submits a prompt and exposes
	// `prompt` in its payload, so it is wired as the prompt-capture
	// source for Gemini (parity with Claude UserPromptSubmit and
	// Codex UserPromptSubmit).
	// PreCompress fires asynchronously before Gemini compresses its
	// chat history. The payload only carries `trigger` (auto / manual)
	// — Gemini exposes no post-compress event with the resulting
	// summary, so Traceary records this as a `compact_summary` *marker*
	// (no digest body). source_hook=pre_compact normalizes the host's
	// `PreCompress` name onto the same internal label Claude uses for
	// PreCompact.
	eventOrder := []string{"SessionStart", "SessionEnd", "BeforeAgent", "AfterAgent", "AfterTool", "PreCompress"}
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
		"BeforeAgent": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf(
					"traceary-prompt",
					"command",
					promptCommand,
					timeout,
					"Record user prompt as a prompt event",
					managedKeyOf("traceary-prompt.sh", "gemini"),
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
		"PreCompress": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf(
					"traceary-pre-compress",
					"command",
					preCompressCommand,
					timeout,
					"Record a pre-compact marker (Gemini exposes no post-compress hook)",
					managedKeyOf("traceary-compact.sh", "gemini", "pre-compact"),
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
