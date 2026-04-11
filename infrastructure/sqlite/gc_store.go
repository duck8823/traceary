package sqlite

import (
	"context"
	_ "embed"
	"log/slog"
	"time"

	"golang.org/x/xerrors"
)

//go:embed sql/count_deletable_events.sql
var countDeletableEventsQuery string

//go:embed sql/delete_old_events.sql
var deleteOldEventsQuery string

// CollectGarbage deletes events older than the given time.
func (d *Datasource) CollectGarbage(
	ctx context.Context,
	before time.Time,
	dryRun bool,
) (int, error) {
	db, err := d.openDB(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for garbage collection: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	beforeValue := formatTimestamp(before)

	var deleteCount int
	if err := db.QueryRowContext(
		ctx,
		countDeletableEventsQuery,
		beforeValue,
	).Scan(&deleteCount); err != nil {
		return 0, xerrors.Errorf("failed to count deletable events: %w", err)
	}

	if dryRun || deleteCount == 0 {
		return deleteCount, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, xerrors.Errorf("failed to begin garbage-collection transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	if _, err := tx.ExecContext(
		ctx,
		deleteOldEventsQuery,
		beforeValue,
	); err != nil {
		return 0, xerrors.Errorf("failed to delete old events: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, xerrors.Errorf("failed to commit garbage-collection transaction: %w", err)
	}

	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return 0, xerrors.Errorf("failed to run VACUUM: %w", err)
	}

	return deleteCount, nil
}
