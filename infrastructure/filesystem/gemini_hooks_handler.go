package filesystem

import (
	"path/filepath"

	"github.com/duck8823/traceary/domain/model"
)

// GeminiHooksHandler installs Traceary hooks for the Gemini CLI.
type GeminiHooksHandler struct{}

// NewGeminiHooksHandler constructs a GeminiHooksHandler.
func NewGeminiHooksHandler() *GeminiHooksHandler {
	return &GeminiHooksHandler{}
}

// Name returns the canonical client identifier.
func (h *GeminiHooksHandler) Name() string { return "gemini" }

// Build returns the Hooks aggregate Traceary installs for Gemini CLI.
func (h *GeminiHooksHandler) Build(scriptsDir string, tracearyBin string) model.Hooks {
	return model.NewGeminiHooks(scriptsDir, tracearyBin)
}

// DefaultInstallPath returns the standard Gemini settings path for the given
// project directory.
func (h *GeminiHooksHandler) DefaultInstallPath(projectDir string) (string, error) {
	return filepath.Join(projectDir, ".gemini", "settings.json"), nil
}
