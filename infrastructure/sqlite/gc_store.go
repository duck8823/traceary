package sqlite

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

var _ usecase.GarbageCollector = (*Datasource)(nil)

// CollectGarbage deletes events older than the given time.
func (d *Datasource) CollectGarbage(
	ctx context.Context,
	dbPath string,
	before time.Time,
	dryRun bool,
) (int, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for garbage collection: %w", err)
	}
	defer func() { _ = db.Close() }()

	beforeValue := formatTimestamp(before)

	var deleteCount int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM events WHERE created_at < ?`,
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
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM events WHERE created_at < ?`,
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
