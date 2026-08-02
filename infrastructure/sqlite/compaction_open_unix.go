//go:build unix

//nolint:wrapcheck // Caller adds path and protocol context.
package sqlite

import (
	"os"

	"golang.org/x/sys/unix"
)

func openFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path, flag|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm.Perm()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
