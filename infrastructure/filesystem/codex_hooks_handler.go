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
	usageCommand := newHookRuntimeCommand(tracearyBin, "hook", "usage", "codex")
	compactPreCommand := newHookRuntimeCommand(tracearyBin, "hook", "compact", "codex", "pre-compact")
	compactPostCommand := newHookRuntimeCommand(tracearyBin, "hook", "compact", "codex", "post-compact")
	subagentStartCommand := newHookRuntimeCommand(tracearyBin, "hook", "subagent-start", "codex")
	subagentStopCommand := newHookRuntimeCommand(tracearyBin, "hook", "subagent-stop", "codex")

	eventOrder := []string{"SessionStart", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "UserPromptSubmit", "Stop", "PostToolUse"}
	events := map[string][]model.HookEntry{
		"SessionStart": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-session-start", "command", sessionStartCommand, types.None[int](), "", managedKeyOf("traceary-session.sh", "codex", "start")),
			}),
		},
		"SubagentStart": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-subagent-start", "command", subagentStartCommand, types.None[int](), "", managedKeyOf("traceary-subagent-start.sh", "codex")),
			}),
		},
		"SubagentStop": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-subagent-stop", "command", subagentStopCommand, types.None[int](), "", managedKeyOf("traceary-subagent-stop.sh", "codex")),
			}),
		},
		"PreCompact": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-compact-pre-compact", "command", compactPreCommand, types.None[int](), "", managedKeyOf("traceary-compact.sh", "codex", "pre-compact")),
			}),
		},
		"PostCompact": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-compact-post-compact", "command", compactPostCommand, types.None[int](), "", managedKeyOf("traceary-compact.sh", "codex", "post-compact")),
			}),
		},
		"UserPromptSubmit": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-prompt", "command", promptCommand, types.None[int](), "", managedKeyOf("traceary-prompt.sh", "codex")),
			}),
		},
		// Codex delivers `last_assistant_message` on Stop, so usage and
		// transcript capture run alongside the session-stop hook in the
		// same event entry. Stop fires after every assistant response,
		// not when the conversation ends, so the session-stop hook is a
		// turn boundary: it keeps the session open and the hook state
		// intact, and only fires the best-effort memory auto-extract
		// (#1170). Ordering is deliberate: body-free usage is read first,
		// transcript is recorded next, and both complete before any
		// turn-boundary side effects, keeping the event order
		// chronologically accurate.
		"Stop": {
			model.HookEntryOf(types.None[string](), []model.HookCommand{
				model.HookCommandOf("traceary-usage", "command", usageCommand, types.None[int](), "", managedKeyOf("traceary-usage.sh", "codex")),
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
