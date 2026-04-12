package filesystem

import (
	"path/filepath"

	"github.com/duck8823/traceary/domain/model"
)

// ClaudeHooksHandler installs Traceary hooks for the Claude Code client.
type ClaudeHooksHandler struct{}

// NewClaudeHooksHandler constructs a ClaudeHooksHandler.
func NewClaudeHooksHandler() *ClaudeHooksHandler {
	return &ClaudeHooksHandler{}
}

// Name returns the canonical client identifier.
func (h *ClaudeHooksHandler) Name() string { return "claude" }

// Build returns the Hooks aggregate Traceary installs for Claude Code.
func (h *ClaudeHooksHandler) Build(scriptsDir string, tracearyBin string) model.Hooks {
	return model.NewClaudeHooks(scriptsDir, tracearyBin)
}

// DefaultInstallPath returns the standard Claude Code settings path for the
// given project directory.
func (h *ClaudeHooksHandler) DefaultInstallPath(projectDir string) (string, error) {
	return filepath.Join(projectDir, ".claude", "settings.json"), nil
}
