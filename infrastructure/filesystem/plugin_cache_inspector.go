package filesystem

import "github.com/duck8823/traceary/application"

// PluginCacheInspector implements application.PluginCacheInspector by
// delegating to the existing filesystem-level detector. It adapts the
// infrastructure value-object to the application type so presentation
// code depends only on the application package.
type PluginCacheInspector struct{}

// NewPluginCacheInspector constructs a PluginCacheInspector.
func NewPluginCacheInspector() *PluginCacheInspector {
	return &PluginCacheInspector{}
}

// DetectClaudePluginCacheStatus resolves the cached plugin version
// and the marketplace plugin.json version for the given pluginKey
// (of the form "<plugin>@<marketplace>").
func (i *PluginCacheInspector) DetectClaudePluginCacheStatus(home, pluginKey string) application.PluginCacheStatus {
	status := DetectClaudePluginCacheStatus(home, pluginKey)
	return application.PluginCacheStatus{
		CachePath:          status.CachePath,
		CachedVersion:      status.CachedVersion,
		CachedVersions:     status.CachedVersions,
		MarketplaceVersion: status.MarketplaceVersion,
		MarketplacePath:    status.MarketplacePath,
	}
}
