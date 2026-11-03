//go:build unix

package runtime

import (
	"errors"
	"fmt"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
)

// privateRuntimeDir never follows symlinks, and never repairs permissions on pre-existing paths.
func privateRuntimeDir(root string, create bool) (int, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return -1, domain.ErrInvalid
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, domain.ErrUnsafe
	}
	parts := strings.Split(strings.TrimPrefix(root, "/"), "/")
	for i, part := range parts {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(e, unix.ENOENT) && create {
			if e = unix.Mkdirat(fd, part, 0700); e != nil && !errors.Is(e, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, domain.ErrUnsafe
			}
			next, e = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		_ = unix.Close(fd)
		if e != nil {
			return -1, domain.ErrUnsafe
		}
		fd = next
		var stat unix.Stat_t
		if e = unix.Fstat(fd, &stat); e != nil {
			_ = unix.Close(fd)
			return -1, domain.ErrUnsafe
		}
		uid := uint32(os.Geteuid())
		trusted := stat.Uid == uid || stat.Uid == 0
		writable := stat.Mode&0022 != 0
		trustedSticky := stat.Uid == 0 && stat.Mode&unix.S_ISVTX != 0
		if !trusted || writable && !trustedSticky || i == len(parts)-1 && (stat.Uid != uid || stat.Mode&0777 != 0700) {
			_ = unix.Close(fd)
			return -1, domain.ErrUnsafe
		}
	}
	return fd, nil
}

// directoryWitness derives identity from the no-follow-walked descriptor, never
// from a separate path lookup that could name a replacement directory.
func directoryWitness(fd int) (string, error) {
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0777 != 0700 {
		return "", domain.ErrUnsafe
	}
	return fmt.Sprintf("%d:%d:%d", stat.Uid, stat.Dev, stat.Ino), nil
}
func verifyRuntimeRoot(root, witness string, dir int) error {
	current, err := privateRuntimeDir(root, false)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(current) }()
	opened, err := directoryWitness(dir)
	if err != nil {
		return err
	}
	named, err := directoryWitness(current)
	if err != nil {
		return err
	}
	if opened != witness || named != witness {
		return domain.ErrChanged
	}
	return nil
}
