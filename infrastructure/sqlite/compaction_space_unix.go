//go:build unix

package sqlite

import "golang.org/x/sys/unix"

func availableBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	if stat.Bavail < 0 {
		return 0, nil
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
