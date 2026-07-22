//go:build darwin

package filesystem

import (
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

func renameFileRetentionNoReplace(rootFD int, oldName, newName string) error {
	if err := unix.RenameatxNp(rootFD, oldName, rootFD, newName, unix.RENAME_EXCL); err != nil {
		return xerrors.Errorf("rename file retention name without replacement: %w", err)
	}
	return nil
}
