package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

const hookSessionGCInterval = 6 * time.Hour
const hookSessionActivityLeaseTTL = time.Minute
const hookSessionActivityLeasePruneTTL = 5 * time.Minute

// runOpportunisticSessionGC keeps abandoned hook sessions bounded without
// adding a scheduler or making host hooks depend on maintenance succeeding.
// The marker is keyed by database path so concurrent clients sharing one store
// perform at most one pass per interval.
func (c *RootCLI) runOpportunisticSessionGC(ctx context.Context, dbPath string, currentSessionID types.SessionID) {
	if c.storeManagement == nil || strings.TrimSpace(dbPath) == "" {
		return
	}
	if err := recordHookSessionActivity(currentSessionID); err != nil {
		slog.Debug("opportunistic session GC activity registration failed", "error", err)
		return
	}
	markerPath, err := hookSessionGCMarkerPath(dbPath)
	if err != nil {
		slog.Debug("opportunistic session GC marker resolution failed", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
		slog.Debug("opportunistic session GC state directory creation failed", "error", err)
		return
	}
	now := time.Now()
	if hookSessionGCRecentlyCompleted(markerPath, now) {
		return
	}
	lease := flock.New(markerPath + ".lock")
	locked, err := lease.TryLock()
	if err != nil || !locked {
		if err != nil {
			slog.Debug("opportunistic session GC lease failed", "error", err)
		}
		return
	}
	defer func() {
		if err := lease.Unlock(); err != nil {
			slog.Debug("opportunistic session GC lease release failed", "error", err)
		}
	}()

	if hookSessionGCRecentlyCompleted(markerPath, now) {
		return
	}
	protectedSessionIDs, err := activeHookSessionIDs(now)
	if err != nil {
		slog.Debug("opportunistic session GC active-session discovery failed", "error", err)
		return
	}
	protectedSessionIDs = append(protectedSessionIDs, currentSessionID)

	result, err := c.storeManagement.CloseStaleSessions(ctx, defaultActiveSessionStaleAfter, false, protectedSessionIDs)
	if err != nil {
		slog.Debug("opportunistic session GC failed", "error", err)
		return
	}
	if err := os.WriteFile(markerPath, []byte(now.UTC().Format(time.RFC3339Nano)), 0o600); err != nil {
		slog.Debug("opportunistic session GC marker write failed", "error", err)
		return
	}
	slog.Debug("opportunistic session GC completed", "closed_sessions", result.ClosedCount())
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
	if err := os.WriteFile(path, []byte(sessionID), 0o600); err != nil {
		return xerrors.Errorf("failed to write hook session activity lease: %w", err)
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

func hookSessionGCMarkerPath(dbPath string) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(dbPath)
	if trimmed == "" {
		return "", xerrors.Errorf("database path is empty")
	}
	digest := sha256.Sum256([]byte(trimmed))
	return filepath.Join(stateDir, "session-gc", hex.EncodeToString(digest[:])+".stamp"), nil
}

func hookSessionGCRecentlyCompleted(markerPath string, now time.Time) bool {
	info, err := os.Stat(markerPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("opportunistic session GC marker inspection failed", "error", err)
		}
		return false
	}
	return now.Sub(info.ModTime()) < hookSessionGCInterval
}
