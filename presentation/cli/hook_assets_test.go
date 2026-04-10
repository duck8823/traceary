package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureHookScriptsInstalled_MaterializesCanonicalScripts(t *testing.T) {
	homeDir := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})
	t.Cleanup(ResetUserHomeDirFunc)

	scriptsDir, err := ensureHookScriptsInstalled()
	if err != nil {
		t.Fatalf("ensureHookScriptsInstalled() error = %v", err)
	}

	if got, want := scriptsDir, filepath.Join(homeDir, ".config", "traceary", "hook-scripts"); got != want {
		t.Fatalf("scriptsDir = %q, want %q", got, want)
	}

	repoRoot := filepath.Join("..", "..")
	for _, asset := range hookScriptAssets {
		outputPath := filepath.Join(scriptsDir, asset.Name)
		gotContent, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", outputPath, err)
		}
		if got, want := string(gotContent), asset.Content; got != want {
			t.Fatalf("installed asset %q mismatch", asset.Name)
		}

		canonicalPath := filepath.Join(repoRoot, "scripts", "hooks", asset.Name)
		canonicalContent, err := os.ReadFile(canonicalPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", canonicalPath, err)
		}
		if got, want := asset.Content, string(canonicalContent); got != want {
			t.Fatalf("asset %q drifted from canonical script", asset.Name)
		}
	}
}
