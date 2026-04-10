package sqlite

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/xerrors"
)

// CloseStaleSessionsInput defines the criteria for closing stale sessions.
type CloseStaleSessionsInput struct {
	StaleAfter time.Duration
	DryRun     bool
}

// CloseStaleSessionsResult contains the count of closed sessions.
type CloseStaleSessionsResult struct {
	ClosedCount int
}

// CloseStaleSessions closes active sessions that have no recent events.
func (d *Datasource) CloseStaleSessions(
	ctx context.Context,
	dbPath string,
	input CloseStaleSessionsInput,
) (*CloseStaleSessionsResult, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	cutoff := formatTimestamp(time.Now().Add(-input.StaleAfter))

	if input.DryRun {
		var count int
		if err := db.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL AND started_at < ?`,
			cutoff,
		).Scan(&count); err != nil {
			return nil, xerrors.Errorf("failed to count stale sessions: %w", err)
		}
		return &CloseStaleSessionsResult{ClosedCount: count}, nil
	}

	now := formatTimestamp(time.Now())
	result, err := db.ExecContext(
		ctx,
		`UPDATE sessions SET ended_at = ? WHERE ended_at IS NULL AND started_at < ?`,
		now, cutoff,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to close stale sessions: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, xerrors.Errorf("failed to check rows affected: %w", err)
	}

	return &CloseStaleSessionsResult{ClosedCount: int(rowsAffected)}, nil
}
