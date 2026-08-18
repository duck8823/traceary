package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/xerrors"
)

// Must stay aligned with infrastructure/sqlite compactPendingSuffix.
const storeCompactPendingSuffix = ".traceary.compact-pending"

func storeCompactPendingActive(dbPath string) bool {
	path, err := storeCompactPendingMarkerPath(dbPath)
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if convErr != nil || pid <= 0 {
		return true
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	if process.Signal(syscall.Signal(0)) != nil {
		_ = os.Remove(path)
		return false
	}
	return true
}

func storeCompactPendingMarkerPath(dbPath string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(dbPath))
	if err != nil {
		return "", xerrors.Errorf("resolve compact-pending marker path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved + storeCompactPendingSuffix, nil
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", xerrors.Errorf("resolve compact-pending marker directory: %w", err)
	}
	return filepath.Join(parent, filepath.Base(absolute)) + storeCompactPendingSuffix, nil
}
