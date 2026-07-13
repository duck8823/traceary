//go:build !unix

package filesystem

import (
	"os"
	"path/filepath"

	"golang.org/x/xerrors"
)

func safeWriteFileAtomic(absPath string, data []byte, fallbackPerm os.FileMode) (retErr error) {
	if !filepath.IsAbs(absPath) {
		return xerrors.Errorf("path must be absolute: %s", absPath)
	}
	perm := fallbackPerm.Perm()
	if info, err := os.Stat(absPath); err == nil {
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return xerrors.Errorf("failed to inspect %s: %w", absPath, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(absPath), "."+filepath.Base(absPath)+".tmp-*")
	if err != nil {
		return xerrors.Errorf("failed to create temporary file for %s: %w", absPath, err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(perm); err != nil {
		return xerrors.Errorf("failed to preserve permissions for %s: %w", absPath, err)
	}
	if _, err := temp.Write(data); err != nil {
		return xerrors.Errorf("failed to write temporary file for %s: %w", absPath, err)
	}
	if err := temp.Sync(); err != nil {
		return xerrors.Errorf("failed to sync temporary file for %s: %w", absPath, err)
	}
	if err := temp.Close(); err != nil {
		return xerrors.Errorf("failed to close temporary file for %s: %w", absPath, err)
	}
	if err := os.Rename(tempPath, absPath); err != nil {
		return xerrors.Errorf("failed to atomically replace %s: %w", absPath, err)
	}
	return nil
}

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
