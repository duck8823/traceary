//go:build linux

package sqlite

import "golang.org/x/sys/unix"

func renameSegmentAtNoReplace(dirFD int, oldName, newName string) error {
	//nolint:wrapcheck // The syscall sentinel (notably EEXIST) is part of the recovery decision.
	return unix.Renameat2(dirFD, oldName, dirFD, newName, unix.RENAME_NOREPLACE)
}
