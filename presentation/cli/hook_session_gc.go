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
)

const hookSessionGCInterval = 6 * time.Hour

// runOpportunisticSessionGC keeps abandoned hook sessions bounded without
// adding a scheduler or making host hooks depend on maintenance succeeding.
// The marker is keyed by database path so concurrent clients sharing one store
// perform at most one pass per interval.
func (c *RootCLI) runOpportunisticSessionGC(ctx context.Context, dbPath string) {
	if c.storeManagement == nil || strings.TrimSpace(dbPath) == "" {
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

	result, err := c.storeManagement.CloseStaleSessions(ctx, defaultActiveSessionStaleAfter, false)
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
