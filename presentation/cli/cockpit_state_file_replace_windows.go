//go:build windows

package cli

import (
	"golang.org/x/sys/windows"
	"golang.org/x/xerrors"
)

func replaceCockpitStateFile(tmpPath string, path string) error {
	from, err := windows.UTF16PtrFromString(tmpPath)
	if err != nil {
		return xerrors.Errorf("failed to encode cockpit state temp path: %w", err)
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return xerrors.Errorf("failed to encode cockpit state path: %w", err)
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return xerrors.Errorf("failed to replace cockpit state: %w", err)
	}
	return nil
}
