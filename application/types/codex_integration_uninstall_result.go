package types

// CodexIntegrationUninstallResult summarizes the files touched during a local
// Codex integration uninstall run.
type CodexIntegrationUninstallResult struct {
	marketplaceCopyPath      string
	marketplaceCopyRemoved   bool
	marketplaceManifestPath  string
	activePluginCachePath    string
	activePluginCacheRemoved bool
	configPath               string
	hooksPath                string
	hooksRemoved             bool
}

// CodexIntegrationUninstallResultOf creates a CodexIntegrationUninstallResult.
func CodexIntegrationUninstallResultOf(
	marketplaceCopyPath string,
	marketplaceCopyRemoved bool,
	marketplaceManifestPath string,
	activePluginCachePath string,
	activePluginCacheRemoved bool,
	configPath string,
	hooksPath string,
	hooksRemoved bool,
) CodexIntegrationUninstallResult {
	return CodexIntegrationUninstallResult{
		marketplaceCopyPath:      marketplaceCopyPath,
		marketplaceCopyRemoved:   marketplaceCopyRemoved,
		marketplaceManifestPath:  marketplaceManifestPath,
		activePluginCachePath:    activePluginCachePath,
		activePluginCacheRemoved: activePluginCacheRemoved,
		configPath:               configPath,
		hooksPath:                hooksPath,
		hooksRemoved:             hooksRemoved,
	}
}

// MarketplaceCopyPath returns the local marketplace plugin path.
func (r CodexIntegrationUninstallResult) MarketplaceCopyPath() string { return r.marketplaceCopyPath }

// MarketplaceCopyRemoved reports whether the marketplace plugin copy existed
// and was removed.
func (r CodexIntegrationUninstallResult) MarketplaceCopyRemoved() bool {
	return r.marketplaceCopyRemoved
}

// MarketplaceManifestPath returns the marketplace manifest path.
func (r CodexIntegrationUninstallResult) MarketplaceManifestPath() string {
	return r.marketplaceManifestPath
}

// ActivePluginCachePath returns the active plugin cache root.
func (r CodexIntegrationUninstallResult) ActivePluginCachePath() string {
	return r.activePluginCachePath
}

// ActivePluginCacheRemoved reports whether the plugin cache existed and was
// removed.
func (r CodexIntegrationUninstallResult) ActivePluginCacheRemoved() bool {
	return r.activePluginCacheRemoved
}

// ConfigPath returns the updated config.toml path.
func (r CodexIntegrationUninstallResult) ConfigPath() string { return r.configPath }

// HooksPath returns the hooks.json path checked during uninstall.
func (r CodexIntegrationUninstallResult) HooksPath() string { return r.hooksPath }

// HooksRemoved reports whether any Traceary-managed Codex hooks were removed.
func (r CodexIntegrationUninstallResult) HooksRemoved() bool { return r.hooksRemoved }
