//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package main

import "golang.org/x/xerrors"

func availableScratchBytes(string) (uint64, error) {
	return 0, xerrors.Errorf("scratch capacity preflight is unavailable on this platform")
}
