package filesystem

import (
	"os"

	"golang.org/x/xerrors"
)

// osUserHomeDir wraps os.UserHomeDir so callers inside the filesystem
// package can use a single function even when they don't need to accept a
// custom lookup function.
func osUserHomeDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", xerrors.Errorf("failed to get user home directory: %w", err)
	}

	return homeDir, nil
}
