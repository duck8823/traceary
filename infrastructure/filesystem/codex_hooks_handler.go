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
// scriptsDir is the directory that contains the hook scripts and
// tracearyBin is the command or path used to launch the traceary binary.
func (h *CodexHooksHandler) Build(scriptsDir string, tracearyBin string) model.Hooks {
	sessionStartCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "codex", "start")
	sessionStopCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "codex", "stop")
	auditCommand := newHookScriptCommand(scriptsDir, tracearyBin, "traceary-audit.sh", "codex")

	eventOrder := []string{"SessionStart", "Stop", "PostToolUse"}
	events := map[string][]model.HookEntry{
		"SessionStart": {
			model.HookEntryOf(types.Empty[string](), []model.HookCommand{
				model.HookCommandOf("", "command", sessionStartCommand, types.Empty[int](), "", managedKeyOf("traceary-session.sh", "codex", "start")),
			}),
		},
		"Stop": {
			model.HookEntryOf(types.Empty[string](), []model.HookCommand{
				model.HookCommandOf("", "command", sessionStopCommand, types.Empty[int](), "", managedKeyOf("traceary-session.sh", "codex", "stop")),
			}),
		},
		"PostToolUse": {
			model.HookEntryOf(types.Of(""), []model.HookCommand{
				model.HookCommandOf("", "command", auditCommand, types.Empty[int](), "", managedKeyOf("traceary-audit.sh", "codex")),
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
