package sqlite

import (
	"context"
	"database/sql"
	"os"

	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

func inspectOneOffRepairRetirement(ctx context.Context, path string) (apptypes.OneOffRepairRetirement, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return apptypes.OneOffRepairRetirement{}, xerrors.Errorf("store does not exist")
		}
		return apptypes.OneOffRepairRetirement{}, xerrors.Errorf("stat store: %w", err)
	}
	db, err := openO1ReadOnly(ctx, path)
	if err != nil {
		return apptypes.OneOffRepairRetirement{}, err
	}
	defer func() { _ = db.Close() }()

	applied, err := loadAppliedMigrations(ctx, db)
	if err != nil {
		if schemaMigrationsTableMissing(err) {
			applied = map[int64]string{}
		} else {
			return apptypes.OneOffRepairRetirement{}, xerrors.Errorf("load applied migrations: %w", err)
		}
	}

	epoch := apptypes.OneOffRepairNeverRan
	if _, ok := applied[78]; ok {
		epoch = apptypes.OneOffRepairRetired
	} else {
		remaining, remainingErr := epochZeroHookUsageRowsExist(ctx, db)
		if remainingErr != nil && !sqliteNoSuchTable(remainingErr) {
			return apptypes.OneOffRepairRetirement{}, xerrors.Errorf("probe epoch-zero hook usage rows: %w", remainingErr)
		}
		if remaining {
			epoch = apptypes.OneOffRepairOutstanding
		}
	}

	exhausted, exhaustedErr := workspaceObservationBackfillIsExhausted(ctx, db)
	if exhaustedErr != nil {
		return apptypes.OneOffRepairRetirement{}, xerrors.Errorf("probe workspace observation exhausted flag: %w", exhaustedErr)
	}
	workspace := apptypes.OneOffRepairOutstanding
	if exhausted {
		workspace = apptypes.OneOffRepairRetired
	}
	return apptypes.OneOffRepairRetirement{Epoch: epoch, Workspace: workspace}, nil
}

func openO1ReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteO1ReadOnlyDSN(path))
	if err != nil {
		return nil, xerrors.Errorf("open O(1) read-only store: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, xerrors.Errorf("ping O(1) read-only store: %w", err)
	}
	return db, nil
}

func epochZeroHookUsageRowsExist(ctx context.Context, db *sql.DB) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM usage_observations AS observation
     WHERE ts_norm(observation.observed_at) = ?
       AND `+epochZeroHookUsageSourceFilter+`
)`, epochZeroHookUsageObservedAt).Scan(&exists)
	if err != nil {
		return false, xerrors.Errorf("exists epoch-zero hook usage rows: %w", err)
	}
	return exists == 1, nil
}
