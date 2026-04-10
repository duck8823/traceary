package sqlite

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

var _ port.StaleSessionCloser = (*Datasource)(nil)

// CloseStaleSessions closes active sessions that have no recent events.
func (d *Datasource) CloseStaleSessions(
	ctx context.Context,
	dbPath string,
	input port.StaleSessionCloserInput,
) (*port.StaleSessionCloserResult, error) {
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
		return &port.StaleSessionCloserResult{ClosedCount: count}, nil
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

	return &port.StaleSessionCloserResult{ClosedCount: int(rowsAffected)}, nil
}
