package sqlite

import (
	"context"
	_ "embed"
	"log/slog"
	"time"

	"golang.org/x/xerrors"
)

//go:embed sql/count_stale_sessions.sql
var countStaleSessionsQuery string

//go:embed sql/update_stale_sessions.sql
var updateStaleSessionsQuery string

// CloseStaleSessions closes active sessions that have no recent events.
func (d *Datasource) CloseStaleSessions(
	ctx context.Context,
	staleAfter time.Duration,
	dryRun bool,
) (int, error) {
	db, err := d.openDB(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	cutoff := formatTimestamp(time.Now().Add(-staleAfter))

	if dryRun {
		var count int
		if err := db.QueryRowContext(
			ctx,
			countStaleSessionsQuery,
			cutoff,
		).Scan(&count); err != nil {
			return 0, xerrors.Errorf("failed to count stale sessions: %w", err)
		}
		return count, nil
	}

	now := formatTimestamp(time.Now())
	result, err := db.ExecContext(
		ctx,
		updateStaleSessionsQuery,
		now, cutoff,
	)
	if err != nil {
		return 0, xerrors.Errorf("failed to close stale sessions: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, xerrors.Errorf("failed to check rows affected: %w", err)
	}

	return int(rowsAffected), nil
}
