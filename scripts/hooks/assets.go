package hooks

import (
	"embed"
	"io/fs"
	"strings"

	"golang.org/x/xerrors"
)

// ScriptAsset describes one packaged hook script.
type ScriptAsset struct {
	Name    string
	Content string
}

//go:embed *.sh
var scriptAssetsFS embed.FS

// Assets returns the canonical hook scripts bundled with Traceary.
func Assets() ([]ScriptAsset, error) {
	entries, err := fs.ReadDir(scriptAssetsFS, ".")
	if err != nil {
		return nil, xerrors.Errorf("failed to read embedded hook scripts: %w", err)
	}

	assets := make([]ScriptAsset, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		content, err := scriptAssetsFS.ReadFile(entry.Name())
		if err != nil {
			return nil, xerrors.Errorf("failed to read embedded hook script %s: %w", entry.Name(), err)
		}
		assets = append(assets, ScriptAsset{
			Name:    entry.Name(),
			Content: normalizeScriptContent(string(content)),
		})
	}

	return assets, nil
}

func normalizeScriptContent(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(normalized, "\r", "\n")
}
