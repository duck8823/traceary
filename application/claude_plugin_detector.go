package application

// ClaudePluginDetector resolves whether the Traceary Claude Code plugin
// is enabled for the given host home directory. Presentation callers
// depend on this interface so the presentation layer stays free of
// direct infrastructure imports.
type ClaudePluginDetector interface {
	DetectClaudeTracearyPluginIn(home string) ClaudePluginDetection
}

// ClaudePluginDetection reports whether the Traceary Claude Code
// plugin is enabled in the user's global Claude settings.
type ClaudePluginDetection struct {
	// Active is true when the user's ~/.claude/settings.json lists a
	// Traceary plugin under enabledPlugins with value true.
	Active bool
	// SettingsPath is the absolute path that was consulted.
	SettingsPath string
	// PluginKey is the enabledPlugins key that matched, e.g.
	// "traceary@traceary-plugins". Empty when Active is false.
	PluginKey string
}
