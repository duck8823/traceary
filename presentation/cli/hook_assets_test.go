package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookScriptAssetsStayInSyncWithCanonicalScripts(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..")
	for _, asset := range hookScriptAssets {
		canonicalPath := filepath.Join(repoRoot, "scripts", "hooks", asset.name)
		canonicalContent, err := os.ReadFile(canonicalPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", canonicalPath, err)
		}
		if got, want := asset.content, string(canonicalContent); got != want {
			t.Fatalf("asset %q drifted from canonical script", asset.name)
		}
	}
}
