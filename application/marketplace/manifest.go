// Package marketplace provides helpers for reading local plugin marketplace
// manifests shared by host-specific integration flows.
package marketplace

import (
	"encoding/json"
	"os"
	"strings"

	"golang.org/x/xerrors"
)

// ReadManifestVersion reads the top-level version field from a plugin or
// extension manifest. The helper is intentionally package-format agnostic so
// Claude, Codex, and Gemini integration checks can share one parser.
func ReadManifestVersion(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", xerrors.Errorf("failed to read manifest: %w", err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		return "", xerrors.Errorf("failed to parse manifest: %w", err)
	}
	return strings.TrimSpace(manifest.Version), nil
}
