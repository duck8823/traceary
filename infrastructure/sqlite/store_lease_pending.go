//nolint:wrapcheck // Marker helpers preserve path-level errors for lease callers.
package sqlite

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

const compactPendingSuffix = ".traceary.compact-pending"

// CompactPendingMarkerPath is the internal file adjacent to the store lease.
func CompactPendingMarkerPath(storePath string) (string, error) {
	canonical, err := canonicalStorePath(storePath)
	if err != nil {
		return "", err
	}
	return canonical + compactPendingSuffix, nil
}

// CompactPendingActive reports whether compact (or another exclusive waiter)
// asked extract workers to finish the current job and not start another.
// A marker whose pid is not alive is treated as stale and removed.
func CompactPendingActive(storePath string) bool {
	path, err := CompactPendingMarkerPath(storePath)
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
	if !pidAlive(pid) {
		_ = os.Remove(path)
		return false
	}
	return true
}

func writeCompactPendingMarker(storePath string) error {
	path, err := CompactPendingMarkerPath(storePath)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

func clearCompactPendingMarker(storePath string) {
	path, err := CompactPendingMarkerPath(storePath)
	if err != nil {
		return
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return
	}
	if strings.TrimSpace(string(raw)) != strconv.Itoa(os.Getpid()) {
		return
	}
	_ = os.Remove(path)
}

func pidAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
