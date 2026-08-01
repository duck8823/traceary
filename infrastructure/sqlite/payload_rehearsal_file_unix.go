//go:build unix

package sqlite

import (
	"fmt"
	"os"
	"syscall"
)

func physicalFileIdentity(info os.FileInfo) (string, string, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", false
	}
	return fmt.Sprint(stat.Dev), fmt.Sprint(stat.Ino), true
}

func fileLinkCountOne(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

//nolint:wrapcheck // caller converts host details to a fixed headroom error.
func filesystemFreeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
