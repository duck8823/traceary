package types

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"
)

// FileRetentionLineageFromPath derives a stable local-store lineage identifier.
// It identifies a configured source store, not one content snapshot.
func FileRetentionLineageFromPath(class, path string) (string, error) {
	if class != "archive" && class != "backup" {
		return "", xerrors.Errorf("unsupported file retention lineage class %q", class)
	}
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return "", xerrors.Errorf("resolve file retention lineage path: %w", err)
	}
	digest := sha256.Sum256([]byte("file-retention-lineage/v1\x00" + class + "\x00" + filepath.Clean(absolute)))
	return hex.EncodeToString(digest[:]), nil
}
