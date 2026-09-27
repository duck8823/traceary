package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

const hookSessionActivityLeaseTTL = time.Minute
const hookSessionActivityLeasePruneTTL = 5 * time.Minute

// maintainHookActivityLeases preserves capture/cleanup leases without synthetic closure.
func (c *RootCLI) maintainHookActivityLeases(currentSessionID types.SessionID) {
	// Preserve activity leases used by delivery/cleanup protection, but never
	// synthesize a boundary merely because recorded work is idle.
	if err := recordHookSessionActivity(currentSessionID); err != nil {
		slog.Debug("hook activity registration failed", "error", err)
	}
	if _, err := activeHookSessionIDs(time.Now()); err != nil {
		slog.Debug("hook activity lease cleanup failed", "error", err)
	}
}

func recordHookSessionActivity(sessionID types.SessionID) error {
	if sessionID == "" {
		return nil
	}
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return err
	}
	activityDir := filepath.Join(stateDir, "session-activity")
	if err := os.MkdirAll(activityDir, 0o700); err != nil {
		return xerrors.Errorf("failed to create hook session activity directory: %w", err)
	}
	digest := sha256.Sum256([]byte(sessionID))
	path := filepath.Join(activityDir, hex.EncodeToString(digest[:])+".lease")
	if err := writeHookSessionActivityLease(activityDir, path, []byte(sessionID)); err != nil {
		return xerrors.Errorf("failed to write hook session activity lease: %w", err)
	}
	return nil
}

// writeHookSessionActivityLease publishes a complete lease with a
// same-directory atomic rename. os.WriteFile truncates an existing lease
// before writing, which lets a concurrent GC scan observe an empty session ID
// and close a session that is actively refreshing its lease.
func writeHookSessionActivityLease(activityDir, path string, data []byte) error {
	tmp, err := os.CreateTemp(activityDir, ".activity-lease-*")
	if err != nil {
		return xerrors.Errorf("create temporary activity lease: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return xerrors.Errorf("set temporary activity lease permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return xerrors.Errorf("write temporary activity lease: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return xerrors.Errorf("close temporary activity lease: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return xerrors.Errorf("publish activity lease: %w", err)
	}
	return nil
}

func activeHookSessionIDs(now time.Time) ([]types.SessionID, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return nil, err
	}
	activityDir := filepath.Join(stateDir, "session-activity")
	entries, err := os.ReadDir(activityDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, xerrors.Errorf("failed to read hook session activity directory: %w", err)
	}
	result := make([]types.SessionID, 0, len(entries))
	seen := make(map[types.SessionID]struct{}, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".lease") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, xerrors.Errorf("failed to inspect hook session activity lease: %w", err)
		}
		path := filepath.Join(activityDir, entry.Name())
		leaseAge := now.Sub(info.ModTime())
		if leaseAge >= hookSessionActivityLeaseTTL {
			if leaseAge >= hookSessionActivityLeasePruneTTL {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					slog.Debug("stale hook session activity lease cleanup failed", "error", err)
				}
			}
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, xerrors.Errorf("failed to read hook session activity lease: %w", err)
		}
		sessionID := types.SessionID(strings.TrimSpace(string(data)))
		if sessionID == "" {
			continue
		}
		if _, ok := seen[sessionID]; ok {
			continue
		}
		seen[sessionID] = struct{}{}
		result = append(result, sessionID)
	}
	return result, nil
}
