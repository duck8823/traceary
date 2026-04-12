//go:build !unix

package filesystem

import (
	"os"

	"golang.org/x/xerrors"
)

// safeMkdirAll creates the directory tree at absPath. The Unix
// implementation pins every component with O_NOFOLLOW; on non-Unix
// platforms we fall back to os.MkdirAll because the O_NOFOLLOW-
// equivalent guarantees are not available via the Go standard library.
// Callers must ensure untrusted paths are not passed on these
// platforms.
func safeMkdirAll(absPath string, perm os.FileMode) error {
	if err := os.MkdirAll(absPath, perm); err != nil {
		return xerrors.Errorf("failed to mkdir %s: %w", absPath, err)
	}
	return nil
}

// safeReadFile reads the file at absPath. See safeMkdirAll for the
// non-Unix caveat.
func safeReadFile(absPath string) ([]byte, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// safeWriteFile writes data to absPath. See safeMkdirAll for the
// non-Unix caveat.
func safeWriteFile(absPath string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(absPath, data, perm); err != nil {
		return xerrors.Errorf("failed to write %s: %w", absPath, err)
	}
	return nil
}

// safeChmod changes the permission bits of absPath. See safeMkdirAll
// for the non-Unix caveat.
func safeChmod(absPath string, perm os.FileMode) error {
	if err := os.Chmod(absPath, perm); err != nil {
		return xerrors.Errorf("failed to chmod %s: %w", absPath, err)
	}
	return nil
}
