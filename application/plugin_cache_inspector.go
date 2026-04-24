package application

import "golang.org/x/mod/semver"

// PluginCacheInspector reports the state of the host's Traceary plugin
// cache (currently only Claude Code has this concept). Implementations
// live in the infrastructure layer so presentation code depends only
// on this interface.
type PluginCacheInspector interface {
	// DetectClaudePluginCacheStatus resolves the cached plugin version
	// and the marketplace plugin.json version for the given pluginKey
	// (of the form "<plugin>@<marketplace>"). Missing caches and
	// missing marketplace clones are reported via empty fields rather
	// than as errors; only structural IO errors surface through the
	// returned struct.
	DetectClaudePluginCacheStatus(home, pluginKey string) PluginCacheStatus
}

// PluginCacheStatus describes the cached Traceary plugin version
// alongside the version the marketplace currently advertises. When
// the two drift (e.g. operator ran `brew upgrade traceary` without
// `claude plugins update`), the host continues to run an older hook
// set than the CLI supports.
type PluginCacheStatus struct {
	// CachePath is the directory scanned for cached plugin versions.
	// Empty when detection could not resolve it.
	CachePath string
	// CachedVersion is the highest version found under CachePath.
	// Empty when no versioned directory exists (plugin never cached).
	CachedVersion string
	// CachedVersions lists every non-orphaned versioned cache
	// directory found under CachePath, sorted from highest to lowest
	// by semver. Diagnostics use the full list to detect the
	// session-snapshot ambiguity reported in #670.
	CachedVersions []string
	// MarketplaceVersion is the plugin.json "version" Claude Code
	// sees via `~/.claude/plugins/marketplaces/<marketplace>/...`.
	// Empty when the marketplace clone is missing.
	MarketplaceVersion string
	// MarketplacePath is the plugin.json path that was read (empty if
	// the read failed).
	MarketplacePath string
}

// HasMultipleCachedVersions reports whether the plugin's cache
// directory holds more than one non-orphaned version subdirectory.
// When true, a resumed Claude Code session could have snapshotted its
// hook registry against any of them, so operators should restart
// without `--continue` to guarantee the newest hooks are live.
func (s PluginCacheStatus) HasMultipleCachedVersions() bool {
	return len(s.CachedVersions) > 1
}

// Stale reports whether the cached plugin is at an older semver than
// the marketplace currently offers. Returns false when either version
// is unresolved — a missing cache or missing marketplace is surfaced
// to the caller through the struct fields rather than as "stale".
func (s PluginCacheStatus) Stale() bool {
	if s.CachedVersion == "" || s.MarketplaceVersion == "" {
		return false
	}
	return semver.Compare("v"+s.CachedVersion, "v"+s.MarketplaceVersion) < 0
}
