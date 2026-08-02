//go:build linux

//nolint:wrapcheck // Caller adds atomic-exchange protocol context.
package sqlite

import "golang.org/x/sys/unix"

func atomicExchange(left, right string) error {
	return unix.Renameat2(unix.AT_FDCWD, left, unix.AT_FDCWD, right, unix.RENAME_EXCHANGE)
}
