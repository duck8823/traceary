//go:build unix

package runtime

import (
	"errors"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

// openPrivateLock avoids Darwin's observed concurrent create-or-open ENOENT
// window. Existing-open and exclusive-create are separate bounded operations;
// callers still verify owner/mode/link count, path identity and root after flock.
func openPrivateLock(dir int, name string) (int, error) {
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	for range 3 {
		fd, err := unix.Openat(dir, name, flags, 0)
		if !errors.Is(err, unix.ENOENT) {
			if err != nil {
				return -1, xerrors.Errorf("open existing private lock: %w", err)
			}
			return fd, nil
		}
		fd, err = unix.Openat(dir, name, flags|unix.O_CREAT|unix.O_EXCL, 0600)
		if !errors.Is(err, unix.EEXIST) {
			if err != nil {
				return -1, xerrors.Errorf("create exclusive private lock: %w", err)
			}
			return fd, nil
		}
	}
	return -1, unix.EAGAIN
}
