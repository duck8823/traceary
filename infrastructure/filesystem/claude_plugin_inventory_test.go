package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writeClaudePluginInventory writes an installed_plugins.json fixture with
// the given body into the test home and returns its path.
func writeClaudePluginInventory(t *testing.T, home, body string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestListClaudeLocalPluginLeftovers_MissingInventory(t *testing.T) {
	t.Parallel()

	scan, err := ListClaudeLocalPluginLeftovers(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v; want fs.ErrNotExist", err)
	}
	if len(scan.LeftoverPaths) != 0 {
		t.Fatalf("LeftoverPaths = %v; want empty", scan.LeftoverPaths)
	}
}

func TestListClaudeLocalPluginLeftovers_InvalidJSON(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	inventoryPath := writeClaudePluginInventory(t, home, `{not json`)

	scan, err := ListClaudeLocalPluginLeftovers(home)
	if err == nil {
		t.Fatal("error = nil; want parse error")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v; must not look like a missing file", err)
	}
	if scan.InventoryPath != inventoryPath {
		t.Fatalf("InventoryPath = %q; want %q", scan.InventoryPath, inventoryPath)
	}
}

func TestListClaudeLocalPluginLeftovers_ClassifiesRows(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	existingProject := t.TempDir()
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missingA := filepath.Join(home, "gone-a")
	missingB := filepath.Join(home, "gone-b")
	disabledMissing := filepath.Join(home, "gone-disabled")

	inventory := fmt.Sprintf(`{
  "version": 2,
  "plugins": {
    "traceary@traceary-plugins": [
      { "scope": "user", "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.45.0", "version": "0.45.0" },
      { "scope": "local", "projectPath": "%s", "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.45.0", "version": "0.45.0" },
      { "scope": "local", "projectPath": "%s", "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.41.0", "version": "0.41.0" },
      { "scope": "local", "projectPath": "%s", "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.40.0", "version": "0.40.0" },
      { "scope": "local", "projectPath": "%s", "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.39.0", "version": "0.39.0" },
      { "scope": "local", "projectPath": "%s", "enabled": false, "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.39.0", "version": "0.39.0" },
      { "scope": "local", "projectPath": "   ", "installPath": "x", "version": "0.39.0" },
      { "scope": "project", "projectPath": "%s", "installPath": "x", "version": "0.39.0" }
    ],
    "other-plugin@some-marketplace": [
      { "scope": "local", "projectPath": "%s", "installPath": "y", "version": "1.0.0" }
    ]
  }
}`,
		home, existingProject, home,
		missingB, home,
		missingA, home,
		filePath, home,
		disabledMissing, home,
		missingA,
		filepath.Join(home, "gone-other"),
	)
	writeClaudePluginInventory(t, home, inventory)

	scan, err := ListClaudeLocalPluginLeftovers(home)
	if err != nil {
		t.Fatalf("error = %v; want nil", err)
	}

	// Leftovers: missingA, missingB, and the path that exists but is a
	// file. The existing project, the disabled row, the empty projectPath,
	// the project-scope row, and the non-Traceary plugin are all excluded.
	want := []string{filePath, missingA, missingB}
	sort.Strings(want)
	if len(scan.LeftoverPaths) != len(want) {
		t.Fatalf("LeftoverPaths = %v; want %v", scan.LeftoverPaths, want)
	}
	for i, path := range want {
		if scan.LeftoverPaths[i] != path {
			t.Fatalf("LeftoverPaths[%d] = %q; want %q (sorted)", i, scan.LeftoverPaths[i], path)
		}
	}
}

func TestListClaudeLocalPluginLeftovers_NoLeftoversWhenAllPresent(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	existingProject := t.TempDir()
	inventory := fmt.Sprintf(`{
  "version": 2,
  "plugins": {
    "traceary@traceary-plugins": [
      { "scope": "user", "installPath": "x", "version": "0.45.0" },
      { "scope": "local", "projectPath": "%s", "installPath": "y", "version": "0.45.0" }
    ]
  }
}`, existingProject)
	writeClaudePluginInventory(t, home, inventory)

	scan, err := ListClaudeLocalPluginLeftovers(home)
	if err != nil {
		t.Fatalf("error = %v; want nil", err)
	}
	if len(scan.LeftoverPaths) != 0 {
		t.Fatalf("LeftoverPaths = %v; want empty", scan.LeftoverPaths)
	}
}
