package cli

import (
	"github.com/duck8823/traceary/application"
)

// detectClaudeTracearyPluginForCLI resolves the user's home directory
// through userHomeDirFunc (which tests can override) and returns
// whether the Claude Code plugin for Traceary is enabled. A nil
// detector (e.g. unit tests that did not inject one) falls back to
// Active=false so callers treat the environment as "no plugin".
func (c *RootCLI) detectClaudeTracearyPluginForCLI() application.ClaudePluginDetection {
	if c == nil || c.pluginDetector == nil {
		return application.ClaudePluginDetection{}
	}
	home, err := userHomeDirFunc()
	if err != nil {
		return application.ClaudePluginDetection{}
	}
	return c.pluginDetector.DetectClaudeTracearyPluginIn(home)
}
