package application

import (
	"github.com/duck8823/traceary/domain/model"
)

// HooksClientHandler encapsulates client-specific knowledge required to
// install Traceary hooks for a single client (for example Claude Code, Codex
// CLI, or Gemini CLI).
type HooksClientHandler interface {
	// Name returns the canonical client identifier (e.g. "claude").
	Name() string

	// Build returns the Hooks aggregate Traceary installs for this client.
	// tracearyBin is the command or path used to launch the traceary binary.
	Build(tracearyBin string) model.Hooks

	// DefaultInstallPath returns the standard configuration file path
	// for this client relative to the given project directory.
	DefaultInstallPath(projectDir string) (string, error)
}
