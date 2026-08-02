//go:build unix

package cli

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"math"
	"path/filepath"
)

func inspectDoctorDiskFree(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &stat); err != nil {
		return 0, fmt.Errorf("inspect doctor disk headroom: %w", err)
	}
	bavail, blockSize := uint64(stat.Bavail), uint64(stat.Bsize)
	if bavail > math.MaxInt64 || blockSize > math.MaxInt64 {
		return 0, errors.New("doctor disk headroom fields exceed signed range")
	}
	return checkedDoctorDiskFree(int64(bavail), int64(blockSize))
}

func checkedDoctorDiskFree(availableBlocks, blockSize int64) (int64, error) {
	if availableBlocks < 0 || blockSize <= 0 {
		return 0, errors.New("doctor disk headroom fields are negative or zero")
	}
	if availableBlocks > math.MaxInt64/blockSize {
		return 0, errors.New("doctor disk headroom overflows")
	}
	return availableBlocks * blockSize, nil
}
