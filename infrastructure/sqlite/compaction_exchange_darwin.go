//go:build darwin

//nolint:wrapcheck // Caller adds atomic-exchange protocol context.
package sqlite

import "golang.org/x/sys/unix"

func atomicExchange(left, right string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, left, unix.AT_FDCWD, right, unix.RENAME_SWAP)
}
func atomicExchangeSupported() bool { return true }
func renameNoReplace(left, right string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, left, unix.AT_FDCWD, right, unix.RENAME_EXCL)
}
