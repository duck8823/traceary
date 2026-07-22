//go:build linux

package filesystem

import (
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

func renameFileRetentionNoReplace(rootFD int, oldName, newName string) error {
	if err := unix.Renameat2(rootFD, oldName, rootFD, newName, unix.RENAME_NOREPLACE); err != nil {
		return xerrors.Errorf("rename file retention name without replacement: %w", err)
	}
	return nil
}
