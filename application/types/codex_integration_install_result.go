package types

// CodexIntegrationInstallResult summarizes the files updated by a local Codex
// integration install run.
type CodexIntegrationInstallResult struct {
	marketplaceCopyPath string
	activePluginPath    string
	configPath          string
	hooksPath           string
	pluginID            string
}

// CodexIntegrationInstallResultOf creates a CodexIntegrationInstallResult.
func CodexIntegrationInstallResultOf(
	marketplaceCopyPath string,
	activePluginPath string,
	configPath string,
	hooksPath string,
	pluginID string,
) CodexIntegrationInstallResult {
	return CodexIntegrationInstallResult{
		marketplaceCopyPath: marketplaceCopyPath,
		activePluginPath:    activePluginPath,
		configPath:          configPath,
		hooksPath:           hooksPath,
		pluginID:            pluginID,
	}
}

// MarketplaceCopyPath returns the installed marketplace copy path.
func (r CodexIntegrationInstallResult) MarketplaceCopyPath() string { return r.marketplaceCopyPath }

// ActivePluginPath returns the active Codex plugin cache path.
func (r CodexIntegrationInstallResult) ActivePluginPath() string { return r.activePluginPath }

// ConfigPath returns the updated config.toml path.
func (r CodexIntegrationInstallResult) ConfigPath() string { return r.configPath }

// HooksPath returns the updated hooks.json path.
func (r CodexIntegrationInstallResult) HooksPath() string { return r.hooksPath }

// PluginID returns the enabled Traceary plugin identifier.
func (r CodexIntegrationInstallResult) PluginID() string { return r.pluginID }
