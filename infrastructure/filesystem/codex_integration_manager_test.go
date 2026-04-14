package filesystem_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestCodexIntegrationManager_Install(t *testing.T) {
	t.Parallel()

	manager := filesystem.NewCodexIntegrationManager(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex":  filesystem.NewCodexHooksHandler(),
		"gemini": filesystem.NewGeminiHooksHandler(),
	}))
	repoRoot := newCodexIntegrationRepoRoot(t)
	codexHome := t.TempDir()
	marketplaceRoot := filepath.Join(t.TempDir(), "agents", "plugins")
	marketplaceManifestPath := filepath.Join(marketplaceRoot, "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(marketplaceManifestPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(marketplaceManifestPath, []byte(`{
  "name": "custom-marketplace",
  "interface": {"displayName": "Custom Marketplace"},
  "description": "keep me",
  "plugins": [
    {
      "name": "existing-plugin",
      "source": {"source": "local", "path": "./plugins/existing-plugin"}
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("WriteFile(marketplace) error = %v", err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(hooksPath, []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "custom-cli hook session codex start"
          },
          {
            "name": "traceary-session-start",
            "type": "command",
            "command": "'/tmp/old-traceary' 'hook' 'session' 'codex' 'start'"
          }
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result, err := manager.Install(context.Background(), repoRoot, codexHome, marketplaceRoot, "/tmp/custom-traceary-wrapper")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(result.MarketplaceCopyPath(), ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("marketplace plugin copy missing manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.ActivePluginPath(), ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("active plugin cache missing manifest: %v", err)
	}

	configContent, err := os.ReadFile(result.ConfigPath())
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	if diff := cmp.Diff(true, strings.Contains(string(configContent), "codex_hooks = true")); diff != "" {
		t.Fatalf("config codex_hooks mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(string(configContent), `traceary@local-traceary-plugins`)); diff != "" {
		t.Fatalf("config plugin stanza mismatch (-want +got):\n%s", diff)
	}

	hooksContent, err := os.ReadFile(result.HooksPath())
	if err != nil {
		t.Fatalf("ReadFile(hooks) error = %v", err)
	}
	if diff := cmp.Diff(true, strings.Contains(string(hooksContent), "custom-cli hook session codex start")); diff != "" {
		t.Fatalf("hooks custom command mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, strings.Count(string(hooksContent), "traceary-session-start")); diff != "" {
		t.Fatalf("hooks managed name count mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(string(hooksContent), "/tmp/custom-traceary-wrapper")); diff != "" {
		t.Fatalf("hooks traceary_bin mismatch (-want +got):\n%s", diff)
	}

	marketplaceContent, err := os.ReadFile(marketplaceManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(marketplace) error = %v", err)
	}
	if diff := cmp.Diff(true, strings.Contains(string(marketplaceContent), `"description": "keep me"`)); diff != "" {
		t.Fatalf("marketplace metadata mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(string(marketplaceContent), `"name": "custom-marketplace"`)); diff != "" {
		t.Fatalf("marketplace name mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(string(marketplaceContent), `"name": "existing-plugin"`)); diff != "" {
		t.Fatalf("marketplace existing plugin mismatch (-want +got):\n%s", diff)
	}
}

func TestCodexIntegrationManager_Uninstall(t *testing.T) {
	t.Parallel()

	manager := filesystem.NewCodexIntegrationManager(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex":  filesystem.NewCodexHooksHandler(),
		"gemini": filesystem.NewGeminiHooksHandler(),
	}))
	repoRoot := newCodexIntegrationRepoRoot(t)
	codexHome := t.TempDir()
	marketplaceRoot := filepath.Join(t.TempDir(), "agents", "plugins")

	if _, err := manager.Install(context.Background(), repoRoot, codexHome, marketplaceRoot, "/tmp/custom-traceary-wrapper"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	configPath := filepath.Join(codexHome, "config.toml")
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	configContent = append(configContent, []byte("\n[plugins.\"other-plugin\"]\nenabled = true\n")...)
	if err := os.WriteFile(configPath, configContent, 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	hooksPath := filepath.Join(codexHome, "hooks.json")
	hooks := map[string]interface{}{}
	hooksContent, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("ReadFile(hooks) error = %v", err)
	}
	if err := json.Unmarshal(hooksContent, &hooks); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	hooks["hooks"] = map[string]interface{}{
		"SessionStart": []map[string]interface{}{
			{
				"hooks": []map[string]string{
					{"type": "command", "command": "custom-cli hook session codex start"},
					{"name": "traceary-session-start", "type": "command", "command": "'/tmp/custom-traceary-wrapper' 'hook' 'session' 'codex' 'start'"},
				},
			},
		},
	}
	encodedHooks, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(hooksPath, append(encodedHooks, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(hooks) error = %v", err)
	}

	result, err := manager.Uninstall(context.Background(), codexHome, marketplaceRoot)
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}

	if diff := cmp.Diff(true, result.MarketplaceCopyRemoved()); diff != "" {
		t.Fatalf("MarketplaceCopyRemoved mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, result.ActivePluginCacheRemoved()); diff != "" {
		t.Fatalf("ActivePluginCacheRemoved mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, result.HooksRemoved()); diff != "" {
		t.Fatalf("HooksRemoved mismatch (-want +got):\n%s", diff)
	}

	updatedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(updated config) error = %v", err)
	}
	if diff := cmp.Diff(false, strings.Contains(string(updatedConfig), `traceary@local-traceary-plugins`)); diff != "" {
		t.Fatalf("updated config traceary stanza mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(false, strings.Contains(string(updatedConfig), `[plugins."traceary@local-traceary-plugins".nested]`)); diff != "" {
		t.Fatalf("updated config nested traceary stanza mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(string(updatedConfig), `other-plugin`)); diff != "" {
		t.Fatalf("updated config unrelated stanza mismatch (-want +got):\n%s", diff)
	}

	updatedHooks, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("ReadFile(updated hooks) error = %v", err)
	}
	if diff := cmp.Diff(false, strings.Contains(string(updatedHooks), "traceary-session-start")); diff != "" {
		t.Fatalf("updated hooks managed entry mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(string(updatedHooks), "custom-cli hook session codex start")); diff != "" {
		t.Fatalf("updated hooks unrelated entry mismatch (-want +got):\n%s", diff)
	}
}

func TestCodexIntegrationManager_UninstallDoesNotCreateMarketplaceManifestWhenAbsent(t *testing.T) {
	t.Parallel()

	manager := filesystem.NewCodexIntegrationManager(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex":  filesystem.NewCodexHooksHandler(),
		"gemini": filesystem.NewGeminiHooksHandler(),
	}))
	codexHome := t.TempDir()
	marketplaceRoot := filepath.Join(t.TempDir(), "agents", "plugins")

	result, err := manager.Uninstall(context.Background(), codexHome, marketplaceRoot)
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}

	if diff := cmp.Diff(false, result.MarketplaceCopyRemoved()); diff != "" {
		t.Fatalf("MarketplaceCopyRemoved mismatch (-want +got):\n%s", diff)
	}

	marketplaceManifestPath := filepath.Join(marketplaceRoot, "marketplace.json")
	if _, err := os.Stat(marketplaceManifestPath); !os.IsNotExist(err) {
		t.Fatalf("marketplace manifest should remain absent, stat error = %v", err)
	}
}

func newCodexIntegrationRepoRoot(t *testing.T) string {
	t.Helper()

	repoRoot := t.TempDir()
	pluginManifestPath := filepath.Join(repoRoot, "plugins", "traceary", ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(pluginManifestPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(pluginManifestPath, []byte(`{"name":"traceary","version":"test"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return repoRoot
}

func TestCodexIntegrationManager_InstallReturnsErrorWhenPluginManifestIsMissing(t *testing.T) {
	t.Parallel()

	manager := filesystem.NewCodexIntegrationManager(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex":  filesystem.NewCodexHooksHandler(),
		"gemini": filesystem.NewGeminiHooksHandler(),
	}))
	repoRoot := t.TempDir()
	codexHome := t.TempDir()
	marketplaceRoot := filepath.Join(t.TempDir(), "agents", "plugins")

	_, err := manager.Install(context.Background(), repoRoot, codexHome, marketplaceRoot, "traceary")
	if err == nil {
		t.Fatal("Install() error = nil")
	}
	if diff := cmp.Diff(true, strings.Contains(err.Error(), "Codex plugin package not found")); diff != "" {
		t.Fatalf("Install() error mismatch (-want +got):\n%s", diff)
	}
}

func TestCodexIntegrationManager_InstallReturnsErrorWhenMarketplaceManifestIsInvalid(t *testing.T) {
	t.Parallel()

	manager := filesystem.NewCodexIntegrationManager(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex":  filesystem.NewCodexHooksHandler(),
		"gemini": filesystem.NewGeminiHooksHandler(),
	}))
	repoRoot := newCodexIntegrationRepoRoot(t)
	codexHome := t.TempDir()
	marketplaceRoot := filepath.Join(t.TempDir(), "agents", "plugins")
	marketplaceManifestPath := filepath.Join(marketplaceRoot, "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(marketplaceManifestPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(marketplaceManifestPath, []byte("{not-json}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := manager.Install(context.Background(), repoRoot, codexHome, marketplaceRoot, "traceary")
	if err == nil {
		t.Fatal("Install() error = nil")
	}
	if diff := cmp.Diff(true, strings.Contains(err.Error(), "failed to parse Codex marketplace manifest")); diff != "" {
		t.Fatalf("Install() error mismatch (-want +got):\n%s", diff)
	}
}

func TestCodexIntegrationManager_UninstallReturnsErrorWhenPluginsConfigIsNotATable(t *testing.T) {
	t.Parallel()

	manager := filesystem.NewCodexIntegrationManager(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex":  filesystem.NewCodexHooksHandler(),
		"gemini": filesystem.NewGeminiHooksHandler(),
	}))
	codexHome := t.TempDir()
	marketplaceRoot := filepath.Join(t.TempDir(), "agents", "plugins")
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte("plugins = true\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := manager.Uninstall(context.Background(), codexHome, marketplaceRoot)
	if err == nil {
		t.Fatal("Uninstall() error = nil")
	}
	if diff := cmp.Diff(true, strings.Contains(err.Error(), `Codex config table "plugins" is not an object`)); diff != "" {
		t.Fatalf("Uninstall() error mismatch (-want +got):\n%s", diff)
	}
}
