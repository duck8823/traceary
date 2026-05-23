//go:build !windows

package cli

import (
	"os"

	"golang.org/x/xerrors"
)

func replaceCockpitStateFile(tmpPath string, path string) error {
	if err := os.Rename(tmpPath, path); err != nil {
		return xerrors.Errorf("failed to replace cockpit state: %w", err)
	}
	return nil
}
