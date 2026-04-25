package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectPluginVersion(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"traceary","version":"0.9.0"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	install := doctorPluginInstall{Client: "codex", ManifestPath: manifest, UpdateHint: "reinstall plugin to align"}

	got := inspectPluginVersion(install, "0.10.0")
	if got.Status != doctorStatusWarn {
		t.Fatalf("status = %q, want warn", got.Status)
	}
	if got.Hint != "reinstall plugin to align" || got.FixCommand == "" {
		t.Fatalf("hint/fix missing: %+v", got)
	}
	if !strings.Contains(got.Message, "0.9.0") || !strings.Contains(got.Message, "0.10.0") {
		t.Fatalf("message should include both versions, got %q", got.Message)
	}
}
