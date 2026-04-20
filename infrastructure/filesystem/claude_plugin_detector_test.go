package filesystem

import (
	"os"
	"path/filepath"
	"testing"
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
