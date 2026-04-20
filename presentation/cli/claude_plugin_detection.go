package cli

import (
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

// detectClaudeTracearyPluginForCLI resolves the user's home directory
// through userHomeDirFunc (which tests can override) and returns whether
// the Claude Code plugin for Traceary is enabled. If the home lookup
// fails we fall back to Active=false so callers treat the environment
// as "no plugin" and fall through to the default settings.json flow.
func detectClaudeTracearyPluginForCLI() filesystem.ClaudePluginDetection {
	home, err := userHomeDirFunc()
	if err != nil {
		return filesystem.ClaudePluginDetection{}
	}
	return filesystem.DetectClaudeTracearyPluginIn(home)
}
