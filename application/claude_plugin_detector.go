package application

// ClaudePluginDetector resolves whether the Traceary Claude Code plugin
// is enabled for the given host home directory. Presentation callers
// depend on this interface so the presentation layer stays free of
// direct infrastructure imports.
type ClaudePluginDetector interface {
	DetectClaudeTracearyPluginIn(home string) ClaudePluginDetection
	// ListClaudeLocalPluginLeftovers scans Claude's installed plugin
	// inventory under home and returns the enabled local-scope Traceary
	// installs whose project directory no longer exists. A missing
	// inventory file returns an error matching os.IsNotExist.
	ListClaudeLocalPluginLeftovers(home string) (ClaudeLocalLeftoverScan, error)
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

// ClaudeLocalLeftoverScan reports the enabled, local-scope Traceary
// plugin rows in Claude's installed plugin inventory whose project
// directory no longer exists on disk.
type ClaudeLocalLeftoverScan struct {
	// InventoryPath is the installed_plugins.json file that was read.
	InventoryPath string
	// LeftoverPaths lists the projectPath values of enabled local
	// Traceary installs whose directory is missing (or is not a
	// directory), sorted for stable diagnostics.
	LeftoverPaths []string
}
