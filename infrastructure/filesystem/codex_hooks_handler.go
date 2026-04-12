package filesystem

import (
	"path/filepath"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
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
func (h *CodexHooksHandler) Build(scriptsDir string, tracearyBin string) model.Hooks {
	return model.NewCodexHooks(scriptsDir, tracearyBin)
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
