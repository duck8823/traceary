package filesystem

import (
	"path/filepath"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// CodexHooksHandler installs Traceary hooks for the Codex CLI.
type CodexHooksHandler struct {
	userHomeDir func() (string, error)
}

// NewCodexHooksHandler constructs a CodexHooksHandler using os.UserHomeDir.
func NewCodexHooksHandler() *CodexHooksHandler {
	return &CodexHooksHandler{}
}

// NewCodexHooksHandlerWithHomeDirFunc constructs a CodexHooksHandler with a
// custom home-directory lookup function. Useful for tests that need to
// redirect configuration paths to a temporary directory.
func NewCodexHooksHandlerWithHomeDirFunc(userHomeDir func() (string, error)) *CodexHooksHandler {
	return &CodexHooksHandler{userHomeDir: userHomeDir}
}

// Name returns the canonical client identifier.
func (h *CodexHooksHandler) Name() string { return "codex" }

// Build returns the Hooks aggregate Traceary installs for Codex CLI.
func (h *CodexHooksHandler) Build(tracearyBin string) model.Hooks {
	sessionStartCommand := newHookRuntimeCommand(tracearyBin, "hook", "session", "codex", "start")
	sessionStopCommand := newHookRuntimeCommand(tracearyBin, "hook", "session", "codex", "stop")
	auditCommand := newHookRuntimeCommand(tracearyBin, "hook", "audit", "codex")
	promptCommand := newHookRuntimeCommand(tracearyBin, "hook", "prompt", "codex")
	transcriptCommand := newHookRuntimeCommand(tracearyBin, "hook", "transcript", "codex")

	eventOrder := []string{"SessionStart", "UserPromptSubmit", "Stop", "PostToolUse"}
	events := map[string][]model.HookEntry{
		"SessionStart": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-session-start", "command", sessionStartCommand, types.None[int](), "", managedKeyOf("traceary-session.sh", "codex", "start")),
			}),
		},
		"UserPromptSubmit": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-prompt", "command", promptCommand, types.None[int](), "", managedKeyOf("traceary-prompt.sh", "codex")),
			}),
		},
		// Codex delivers `last_assistant_message` on Stop, so the
		// transcript hook runs alongside the session-stop hook in the
		// same event entry. Stop fires after every assistant response,
		// not when the conversation ends, so the session-stop hook is a
		// turn boundary: it keeps the session open and the hook state
		// intact, and only fires the best-effort memory auto-extract
		// (#1170). Ordering is still deliberate: transcript runs FIRST
		// so the turn's transcript event is recorded before any
		// turn-boundary side effects, keeping the event order
		// chronologically accurate.
		"Stop": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-transcript", "command", transcriptCommand, types.None[int](), "", managedKeyOf("traceary-transcript.sh", "codex")),
				model.HookCommandOf("traceary-session-stop", "command", sessionStopCommand, types.None[int](), "", managedKeyOf("traceary-session.sh", "codex", "stop")),
			}),
		},
		"PostToolUse": {
			model.HookEntryOf(types.Some(""), []model.HookCommand{
				model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "codex")),
			}),
		},
	}

	return model.HooksOf(eventOrder, events)
}

// DefaultInstallPath returns the standard Codex hooks configuration path
// inside the user home directory.
func (h *CodexHooksHandler) DefaultInstallPath(_ string) (string, error) {
	homeDir, err := h.resolveHomeDir()
	if err != nil {
		return "", xerrors.Errorf("failed to get user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".codex", "hooks.json"), nil
}

func (h *CodexHooksHandler) resolveHomeDir() (string, error) {
	if h.userHomeDir != nil {
		return h.userHomeDir()
	}

	return osUserHomeDir()
}
