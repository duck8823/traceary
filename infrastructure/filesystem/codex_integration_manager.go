package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const (
	codexLocalPluginName      = "traceary"
	codexLocalMarketplaceName = "local-traceary-plugins"
	codexLocalPluginID        = codexLocalPluginName + "@" + codexLocalMarketplaceName
	codexLocalPluginVersion   = "local"
)

// CodexIntegrationManager installs and removes the packaged Codex plugin from
// a local repository checkout.
type CodexIntegrationManager struct {
	hooksOrchestrator application.HooksOrchestrator
}

// NewCodexIntegrationManager constructs a CodexIntegrationManager.
func NewCodexIntegrationManager(hooksOrchestrator application.HooksOrchestrator) *CodexIntegrationManager {
	return &CodexIntegrationManager{hooksOrchestrator: hooksOrchestrator}
}

// Install installs the packaged local Codex plugin into the selected Codex home and marketplace roots.
func (m *CodexIntegrationManager) Install(
	ctx context.Context,
	repoRoot string,
	codexHome string,
	marketplaceRoot string,
	tracearyBin string,
) (apptypes.CodexIntegrationInstallResult, error) {
	sourcePlugin := filepath.Join(repoRoot, "plugins", codexLocalPluginName)
	if err := requireCodexPluginSource(sourcePlugin); err != nil {
		return apptypes.CodexIntegrationInstallResult{}, err
	}

	marketplaceCopyPath, err := installCodexMarketplaceCopy(sourcePlugin, marketplaceRoot)
	if err != nil {
		return apptypes.CodexIntegrationInstallResult{}, err
	}
	activePluginPath, err := installCodexPluginCacheCopy(sourcePlugin, codexHome)
	if err != nil {
		return apptypes.CodexIntegrationInstallResult{}, err
	}
	configPath, err := writeCodexIntegrationConfig(codexHome)
	if err != nil {
		return apptypes.CodexIntegrationInstallResult{}, err
	}

	hooksPath := filepath.Join(codexHome, "hooks.json")
	resolvedHooksPath, err := m.hooksOrchestrator.Install(
		ctx,
		"codex",
		tracearyBin,
		repoRoot,
		types.Of(hooksPath),
		false,
	)
	if err != nil {
		return apptypes.CodexIntegrationInstallResult{}, xerrors.Errorf("failed to install Codex hooks: %w", err)
	}

	return apptypes.CodexIntegrationInstallResultOf(
		marketplaceCopyPath,
		activePluginPath,
		configPath,
		resolvedHooksPath,
		codexLocalPluginID,
	), nil
}

// Uninstall removes the packaged local Codex plugin from the selected Codex home and marketplace roots.
func (m *CodexIntegrationManager) Uninstall(
	_ context.Context,
	codexHome string,
	marketplaceRoot string,
) (apptypes.CodexIntegrationUninstallResult, error) {
	marketplaceCopyPath := filepath.Join(marketplaceRoot, "plugins", codexLocalPluginName)
	marketplaceCopyRemoved, err := removePathIfExists(marketplaceCopyPath)
	if err != nil {
		return apptypes.CodexIntegrationUninstallResult{}, xerrors.Errorf("failed to remove marketplace copy: %w", err)
	}

	marketplaceManifestPath := filepath.Join(marketplaceRoot, "marketplace.json")
	if err := removeCodexMarketplaceEntry(marketplaceManifestPath); err != nil {
		return apptypes.CodexIntegrationUninstallResult{}, xerrors.Errorf("failed to update marketplace manifest: %w", err)
	}

	activePluginCachePath := filepath.Join(codexHome, "plugins", "cache", codexLocalMarketplaceName, codexLocalPluginName)
	activePluginCacheRemoved, err := removePathIfExists(activePluginCachePath)
	if err != nil {
		return apptypes.CodexIntegrationUninstallResult{}, xerrors.Errorf("failed to remove active plugin cache: %w", err)
	}

	configPath := filepath.Join(codexHome, "config.toml")
	if err := removeCodexPluginConfig(configPath); err != nil {
		return apptypes.CodexIntegrationUninstallResult{}, xerrors.Errorf("failed to update Codex config: %w", err)
	}

	hooksPath := filepath.Join(codexHome, "hooks.json")
	hooksRemoved, err := removeTracearyManagedCodexHooks(hooksPath)
	if err != nil {
		return apptypes.CodexIntegrationUninstallResult{}, xerrors.Errorf("failed to update Codex hooks: %w", err)
	}

	return apptypes.CodexIntegrationUninstallResultOf(
		marketplaceCopyPath,
		marketplaceCopyRemoved,
		marketplaceManifestPath,
		activePluginCachePath,
		activePluginCacheRemoved,
		configPath,
		hooksPath,
		hooksRemoved,
	), nil
}

func requireCodexPluginSource(path string) error {
	manifestPath := filepath.Join(path, ".codex-plugin", "plugin.json")
	info, err := os.Stat(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return xerrors.Errorf("Codex plugin package not found: %s", manifestPath)
		}
		return xerrors.Errorf("failed to stat Codex plugin package: %w", err)
	}
	if info.IsDir() {
		return xerrors.Errorf("Codex plugin manifest path is a directory: %s", manifestPath)
	}
	return nil
}

func installCodexMarketplaceCopy(sourcePlugin string, marketplaceRoot string) (string, error) {
	targetPlugin := filepath.Join(marketplaceRoot, "plugins", codexLocalPluginName)
	if err := os.MkdirAll(filepath.Dir(targetPlugin), 0o755); err != nil {
		return "", xerrors.Errorf("failed to create marketplace plugin parent: %w", err)
	}
	if err := os.RemoveAll(targetPlugin); err != nil {
		return "", xerrors.Errorf("failed to remove existing marketplace plugin copy: %w", err)
	}
	if err := copyDir(sourcePlugin, targetPlugin); err != nil {
		return "", xerrors.Errorf("failed to copy Codex plugin into marketplace: %w", err)
	}

	marketplacePath := filepath.Join(marketplaceRoot, "marketplace.json")
	marketplace, err := loadCodexMarketplace(marketplacePath)
	if err != nil {
		return "", err
	}
	filtered := make([]map[string]interface{}, 0, len(marketplace))
	for _, entry := range marketplace {
		if entryName, _ := entry["name"].(string); entryName == codexLocalPluginName {
			continue
		}
		filtered = append(filtered, entry)
	}
	filtered = append(filtered, map[string]interface{}{
		"name": codexLocalPluginName,
		"source": map[string]string{
			"source": "local",
			"path":   "./plugins/" + codexLocalPluginName,
		},
		"policy": map[string]string{
			"installation":   "AVAILABLE",
			"authentication": "ON_INSTALL",
		},
		"category": "Coding",
	})
	if err := writeMarketplaceDocument(marketplacePath, filtered); err != nil {
		return "", err
	}

	return targetPlugin, nil
}

func installCodexPluginCacheCopy(sourcePlugin string, codexHome string) (string, error) {
	targetPlugin := filepath.Join(
		codexHome,
		"plugins",
		"cache",
		codexLocalMarketplaceName,
		codexLocalPluginName,
		codexLocalPluginVersion,
	)
	if err := os.RemoveAll(targetPlugin); err != nil {
		return "", xerrors.Errorf("failed to remove existing plugin cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPlugin), 0o755); err != nil {
		return "", xerrors.Errorf("failed to create plugin cache parent: %w", err)
	}
	if err := copyDir(sourcePlugin, targetPlugin); err != nil {
		return "", xerrors.Errorf("failed to copy Codex plugin into active cache: %w", err)
	}
	return targetPlugin, nil
}

func writeCodexIntegrationConfig(codexHome string) (string, error) {
	configPath := filepath.Join(codexHome, "config.toml")
	document, err := loadCodexConfigDocument(configPath)
	if err != nil {
		return "", err
	}

	features, err := ensureTOMLTable(document, "features")
	if err != nil {
		return "", err
	}
	features["codex_hooks"] = true

	plugins, err := ensureTOMLTable(document, "plugins")
	if err != nil {
		return "", err
	}
	pluginConfig, err := ensureTOMLTable(plugins, codexLocalPluginID)
	if err != nil {
		return "", err
	}
	pluginConfig["enabled"] = true

	if err := writeCodexConfigDocument(configPath, document); err != nil {
		return "", err
	}
	return configPath, nil
}

func removeCodexPluginConfig(configPath string) error {
	document, err := loadCodexConfigDocument(configPath)
	if err != nil {
		return err
	}

	pluginsValue, ok := document["plugins"]
	if !ok {
		return nil
	}
	plugins, err := tomlTableFromValue(pluginsValue, "plugins")
	if err != nil {
		return err
	}
	delete(plugins, codexLocalPluginID)
	if len(plugins) == 0 {
		delete(document, "plugins")
	} else {
		document["plugins"] = plugins
	}

	return writeCodexConfigDocument(configPath, document)
}

func removeTracearyManagedCodexHooks(hooksPath string) (bool, error) {
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, xerrors.Errorf("failed to read Codex hooks: %w", err)
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		if err := os.Remove(hooksPath); err != nil && !os.IsNotExist(err) {
			return false, xerrors.Errorf("failed to remove empty hooks file: %w", err)
		}
		return true, nil
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &root); err != nil {
		return false, xerrors.Errorf("hooks.json must contain a JSON object: %w", err)
	}

	hooksValue, ok := root["hooks"]
	if !ok {
		return false, nil
	}

	hooksMap := map[string][]hookMatcherDocument{}
	if err := json.Unmarshal(hooksValue, &hooksMap); err != nil {
		return false, xerrors.Errorf("hooks field must be a JSON object whose values are hook arrays: %w", err)
	}

	removedAny := false
	for eventName, matchers := range hooksMap {
		cleanedMatchers := make([]hookMatcherDocument, 0, len(matchers))
		for _, matcher := range matchers {
			remaining := make([]hookCommandDocument, 0, len(matcher.Hooks))
			for _, command := range matcher.Hooks {
				if isTracearyManagedHookCommandDocument(command, nil) {
					removedAny = true
					continue
				}
				remaining = append(remaining, command)
			}
			if len(remaining) == 0 {
				continue
			}
			matcher.Hooks = remaining
			cleanedMatchers = append(cleanedMatchers, matcher)
		}
		if len(cleanedMatchers) == 0 {
			delete(hooksMap, eventName)
			continue
		}
		hooksMap[eventName] = cleanedMatchers
	}

	if len(hooksMap) == 0 {
		if err := os.Remove(hooksPath); err != nil && !os.IsNotExist(err) {
			return false, xerrors.Errorf("failed to remove hooks file: %w", err)
		}
		return removedAny, nil
	}

	encodedHooks, err := json.MarshalIndent(hooksMap, "", "  ")
	if err != nil {
		return false, xerrors.Errorf("failed to marshal hooks field: %w", err)
	}
	root["hooks"] = encodedHooks

	encodedRoot, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, xerrors.Errorf("failed to marshal hooks JSON: %w", err)
	}
	if err := os.WriteFile(hooksPath, append(encodedRoot, '\n'), 0o644); err != nil {
		return false, xerrors.Errorf("failed to write hooks.json: %w", err)
	}

	return removedAny, nil
}

func removeCodexMarketplaceEntry(marketplacePath string) error {
	entries, err := loadCodexMarketplace(marketplacePath)
	if err != nil {
		return err
	}
	filtered := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		if entryName, _ := entry["name"].(string); entryName == codexLocalPluginName {
			continue
		}
		filtered = append(filtered, entry)
	}
	return writeMarketplaceDocument(marketplacePath, filtered)
}

func loadCodexMarketplace(path string) ([]map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]interface{}{}, nil
		}
		return nil, xerrors.Errorf("failed to read Codex marketplace manifest: %w", err)
	}
	payload := map[string]interface{}{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, xerrors.Errorf("failed to parse Codex marketplace manifest: %w", err)
	}
	rawPlugins, _ := payload["plugins"].([]interface{})
	plugins := make([]map[string]interface{}, 0, len(rawPlugins))
	for _, rawPlugin := range rawPlugins {
		plugin, ok := rawPlugin.(map[string]interface{})
		if !ok {
			continue
		}
		plugins = append(plugins, plugin)
	}
	return plugins, nil
}

func writeMarketplaceDocument(path string, plugins []map[string]interface{}) error {
	payload := map[string]interface{}{
		"name": codexLocalMarketplaceName,
		"interface": map[string]string{
			"displayName": "Local Traceary Plugins",
		},
		"plugins": plugins,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return xerrors.Errorf("failed to create Codex marketplace parent: %w", err)
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to marshal Codex marketplace manifest: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return xerrors.Errorf("failed to write Codex marketplace manifest: %w", err)
	}
	return nil
}

func removePathIfExists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, xerrors.Errorf("failed to stat path %s: %w", path, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return false, xerrors.Errorf("failed to remove path %s: %w", path, err)
	}
	return true, nil
}

func copyDir(source string, destination string) error {
	if err := filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return xerrors.Errorf("failed to walk %s: %w", path, err)
		}
		relativePath, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return xerrors.Errorf("failed to resolve relative path for %s: %w", path, relErr)
		}
		targetPath := filepath.Join(destination, relativePath)
		if info.IsDir() {
			if mkdirErr := os.MkdirAll(targetPath, info.Mode()); mkdirErr != nil {
				return xerrors.Errorf("failed to create directory %s: %w", targetPath, mkdirErr)
			}
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return xerrors.Errorf("failed to read file %s: %w", path, readErr)
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(targetPath), 0o755); mkdirErr != nil {
			return xerrors.Errorf("failed to create parent directory for %s: %w", targetPath, mkdirErr)
		}
		if writeErr := os.WriteFile(targetPath, data, info.Mode()); writeErr != nil {
			return xerrors.Errorf("failed to write file %s: %w", targetPath, writeErr)
		}
		return nil
	}); err != nil {
		return xerrors.Errorf("failed to copy %s to %s: %w", source, destination, err)
	}
	return nil
}

func loadCodexConfigDocument(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil
		}
		return nil, xerrors.Errorf("failed to read Codex config: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return map[string]interface{}{}, nil
	}
	document := map[string]interface{}{}
	if err := toml.Unmarshal(data, &document); err != nil {
		return nil, xerrors.Errorf("failed to parse Codex config: %w", err)
	}
	return document, nil
}

func writeCodexConfigDocument(path string, document map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return xerrors.Errorf("failed to create Codex config parent: %w", err)
	}
	encoded, err := toml.Marshal(document)
	if err != nil {
		return xerrors.Errorf("failed to encode Codex config: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return xerrors.Errorf("failed to write Codex config: %w", err)
	}
	return nil
}

func ensureTOMLTable(document map[string]interface{}, key string) (map[string]interface{}, error) {
	if value, ok := document[key]; ok {
		return tomlTableFromValue(value, key)
	}
	table := map[string]interface{}{}
	document[key] = table
	return table, nil
}

func tomlTableFromValue(value interface{}, key string) (map[string]interface{}, error) {
	table, ok := value.(map[string]interface{})
	if !ok {
		return nil, xerrors.Errorf("Codex config table %q is not an object", key)
	}
	return table, nil
}
