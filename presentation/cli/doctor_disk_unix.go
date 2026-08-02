//go:build unix

package cli

import (
	"fmt"
	"golang.org/x/sys/unix"
	"path/filepath"
)

func inspectDoctorDiskFree(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &stat); err != nil {
		return 0, fmt.Errorf("inspect doctor disk headroom: %w", err)
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
