//go:build unix

package sqlite

import (
	"os"
	"syscall"
)

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
