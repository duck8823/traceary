//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package cli

import (
	"io"
	"os"
	"path/filepath"

	"golang.org/x/xerrors"
)

// readHookSpoolFile uses os.Root so followed links cannot escape the private
// spool directory on platforms without a portable O_NOFOLLOW flag.
func readHookSpoolFile(path string) ([]byte, error) {
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, xerrors.Errorf("failed to open hook spool root: %w", err)
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(name)
	if err != nil {
		return nil, xerrors.Errorf("failed to open hook spool record: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, xerrors.Errorf("failed to inspect hook spool record: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, xerrors.New("hook spool record is not a regular file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, xerrors.Errorf("failed to read hook spool record: %w", err)
	}
	return data, nil
}
