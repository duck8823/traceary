package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDetectClaudeTracearyPluginIn_NoSettingsFile(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	got := DetectClaudeTracearyPluginIn(home)

	if got.Active {
		t.Errorf("Active = true; want false when settings.json is missing")
	}
	wantPath := filepath.Join(home, ".claude", "settings.json")
	if got.SettingsPath != wantPath {
		t.Errorf("SettingsPath = %q; want %q", got.SettingsPath, wantPath)
	}
	if got.PluginKey != "" {
		t.Errorf("PluginKey = %q; want empty", got.PluginKey)
	}
}

func TestDetectClaudeTracearyPluginIn_PluginEnabled(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeClaudeSettings(t, home, `{
  "enabledPlugins": {
    "traceary@traceary-plugins": true,
    "other-plugin@some-marketplace": true
  }
}`)

	got := DetectClaudeTracearyPluginIn(home)

	if !got.Active {
		t.Fatalf("Active = false; want true")
	}
	if got.PluginKey != "traceary@traceary-plugins" {
		t.Errorf("PluginKey = %q; want %q", got.PluginKey, "traceary@traceary-plugins")
	}
}

func TestDetectClaudeTracearyPluginIn_PluginDisabled(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeClaudeSettings(t, home, `{
  "enabledPlugins": {
    "traceary@traceary-plugins": false
  }
}`)

	got := DetectClaudeTracearyPluginIn(home)

	if got.Active {
		t.Errorf("Active = true; want false when plugin is disabled")
	}
}

func TestDetectClaudeTracearyPluginIn_NoTracearyPlugin(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeClaudeSettings(t, home, `{
  "enabledPlugins": {
    "other-plugin@some-marketplace": true
  }
}`)

	got := DetectClaudeTracearyPluginIn(home)

	if got.Active {
		t.Errorf("Active = true; want false when no traceary plugin is listed")
	}
}

func TestDetectClaudeTracearyPluginIn_MalformedJSON(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeClaudeSettings(t, home, `{ not json`)

	got := DetectClaudeTracearyPluginIn(home)

	if got.Active {
		t.Errorf("Active = true; want false when settings.json is malformed")
	}
}

func TestDetectClaudeTracearyPluginIn_MatchesAnyMarketplace(t *testing.T) {
	t.Parallel()

	// A Traceary plugin published under a different marketplace name
	// should still be detected. We only fix the plugin part.
	home := t.TempDir()
	writeClaudeSettings(t, home, `{
  "enabledPlugins": {
    "traceary@fork-marketplace": true
  }
}`)

	got := DetectClaudeTracearyPluginIn(home)

	if !got.Active {
		t.Fatalf("Active = false; want true for traceary@<any>")
	}
	if got.PluginKey != "traceary@fork-marketplace" {
		t.Errorf("PluginKey = %q; want %q", got.PluginKey, "traceary@fork-marketplace")
	}
}

func writeClaudeSettings(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writePluginCache(t *testing.T, home, marketplace, plugin, version string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins", "cache", marketplace, plugin, version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(cache) error = %v", err)
	}
}

func writePluginMarketplaceManifest(t *testing.T, home, marketplace, version string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins", "marketplaces", marketplace, "integrations", "claude-plugin", ".claude-plugin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(manifest) error = %v", err)
	}
	manifest := `{"name":"traceary","version":"` + version + `"}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
}

func TestDetectClaudePluginCacheStatus_StaleCache(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writePluginCache(t, home, "traceary-plugins", "traceary", "0.6.0")
	writePluginMarketplaceManifest(t, home, "traceary-plugins", "0.7.2")

	got := DetectClaudePluginCacheStatus(home, "traceary@traceary-plugins")

	if got.CachedVersion != "0.6.0" {
		t.Errorf("CachedVersion = %q, want 0.6.0", got.CachedVersion)
	}
	if got.MarketplaceVersion != "0.7.2" {
		t.Errorf("MarketplaceVersion = %q, want 0.7.2", got.MarketplaceVersion)
	}
	if !got.Stale() {
		t.Errorf("Stale() = false, want true for 0.6.0 vs 0.7.2")
	}
}

func TestDetectClaudePluginCacheStatus_UpToDateCache(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writePluginCache(t, home, "traceary-plugins", "traceary", "0.7.2")
	writePluginMarketplaceManifest(t, home, "traceary-plugins", "0.7.2")

	got := DetectClaudePluginCacheStatus(home, "traceary@traceary-plugins")

	if got.Stale() {
		t.Errorf("Stale() = true, want false for equal versions")
	}
}

func TestDetectClaudePluginCacheStatus_PicksHighestCachedVersion(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writePluginCache(t, home, "traceary-plugins", "traceary", "0.5.1")
	writePluginCache(t, home, "traceary-plugins", "traceary", "0.7.0")
	writePluginCache(t, home, "traceary-plugins", "traceary", "0.6.1")
	writePluginMarketplaceManifest(t, home, "traceary-plugins", "0.7.2")

	got := DetectClaudePluginCacheStatus(home, "traceary@traceary-plugins")

	if got.CachedVersion != "0.7.0" {
		t.Errorf("CachedVersion = %q, want highest 0.7.0", got.CachedVersion)
	}
	if !got.Stale() {
		t.Errorf("Stale() = false, want true (0.7.0 < 0.7.2)")
	}
	wantVersions := []string{"0.7.0", "0.6.1", "0.5.1"}
	if diff := cmp.Diff(wantVersions, got.CachedVersions); diff != "" {
		t.Errorf("CachedVersions mismatch (-want +got):\n%s", diff)
	}
	if !got.HasMultipleCachedVersions() {
		t.Errorf("HasMultipleCachedVersions() = false, want true (3 versions coexisting)")
	}
}

// TestDetectClaudePluginCacheStatus_SingleCachedVersionIsNotMultiple
// regression test for #670: a single cached version must NOT trigger
// the multi-version warning path. Without this guard the doctor would
// warn every operator even on a clean install.
func TestDetectClaudePluginCacheStatus_SingleCachedVersionIsNotMultiple(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writePluginCache(t, home, "traceary-plugins", "traceary", "0.8.0")
	writePluginMarketplaceManifest(t, home, "traceary-plugins", "0.8.0")

	got := DetectClaudePluginCacheStatus(home, "traceary@traceary-plugins")

	if got.HasMultipleCachedVersions() {
		t.Errorf("HasMultipleCachedVersions() = true, want false for single cached version")
	}
	if diff := cmp.Diff([]string{"0.8.0"}, got.CachedVersions); diff != "" {
		t.Errorf("CachedVersions mismatch (-want +got):\n%s", diff)
	}
}

func TestDetectClaudePluginCacheStatus_IgnoresOrphanedCache(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writePluginCache(t, home, "traceary-plugins", "traceary", "0.7.2")
	// Place a newer but orphaned version alongside.
	writePluginCache(t, home, "traceary-plugins", "traceary", "0.8.0")
	orphanMarker := filepath.Join(home, ".claude", "plugins", "cache", "traceary-plugins", "traceary", "0.8.0", ".orphaned_at")
	if err := os.WriteFile(orphanMarker, []byte("2026-04-21"), 0o600); err != nil {
		t.Fatalf("WriteFile(orphan marker) error = %v", err)
	}
	writePluginMarketplaceManifest(t, home, "traceary-plugins", "0.7.2")

	got := DetectClaudePluginCacheStatus(home, "traceary@traceary-plugins")

	if got.CachedVersion != "0.7.2" {
		t.Errorf("CachedVersion = %q, want 0.7.2 (orphaned 0.8.0 must be ignored)", got.CachedVersion)
	}
	if got.Stale() {
		t.Errorf("Stale() = true, want false (0.7.2 matches marketplace)")
	}
}

func TestDetectClaudePluginCacheStatus_MissingCache(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writePluginMarketplaceManifest(t, home, "traceary-plugins", "0.7.2")

	got := DetectClaudePluginCacheStatus(home, "traceary@traceary-plugins")

	if got.CachedVersion != "" {
		t.Errorf("CachedVersion = %q, want empty", got.CachedVersion)
	}
	if got.Stale() {
		t.Errorf("Stale() = true, want false when cache is absent")
	}
}

func TestDetectClaudePluginCacheStatus_MalformedKey(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	got := DetectClaudePluginCacheStatus(home, "no-at-separator")
	if got.CachePath != "" {
		t.Errorf("CachePath = %q, want empty for malformed key", got.CachePath)
	}
}
