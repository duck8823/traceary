//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

func availableScratchBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, xerrors.Errorf("failed to inspect scratch capacity")
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
