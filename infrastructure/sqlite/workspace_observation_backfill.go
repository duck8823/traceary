package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"golang.org/x/xerrors"
	sqlitelib "modernc.org/sqlite"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const workspaceObservationCatchUpBatchSize = 1000
const workspaceObservationCatchUpMaxAttempts = 3
const workspaceObservationCatchUpRetryDelay = 25 * time.Millisecond

type workspaceObservationCatchUpResult struct {
	Selected    int
	Inserted    int
	Retries     int
	MorePending bool
	Skipped     bool
}

const workspaceObservationCatchUpStateTable = "workspace_observation_catchup_state"

const workspaceObservationCatchUpBatchQuery = `
	SELECT e.id, e.session_id, e.workspace, e.created_at, e.agent,
	       COALESCE(e.source_hook, ''), COALESCE(s.workspace, '')
	  FROM events e
	  LEFT JOIN sessions s ON s.session_id = e.session_id
	 WHERE NOT EXISTS (
	       SELECT 1
	         FROM session_workspace_observations o
	        WHERE o.observed_event_id = e.id
	          AND o.observation_kind = 'primary'
	          AND o.observed_event_id IS NOT NULL
	          AND o.observed_event_id <> ''
	 )
	 ORDER BY e.created_at_norm DESC, e.id DESC
	 LIMIT ?`

func catchUpWorkspaceObservations(ctx context.Context, db *sql.DB, batchSize int) (workspaceObservationCatchUpResult, error) {
	if batchSize <= 0 {
		return workspaceObservationCatchUpResult{}, xerrors.Errorf("workspace observation catch-up batch size must be positive")
	}
	exists, err := sqliteTableExists(ctx, db, "session_workspace_observations")
	if err != nil {
		return workspaceObservationCatchUpResult{}, err
	}
	if !exists {
		return workspaceObservationCatchUpResult{}, nil
	}
	stateExists, err := sqliteTableExists(ctx, db, workspaceObservationCatchUpStateTable)
	if err != nil {
		return workspaceObservationCatchUpResult{}, err
	}
	if stateExists {
		exhausted, exhaustedErr := workspaceObservationCatchUpIsExhausted(ctx, db)
		if exhaustedErr != nil {
			return workspaceObservationCatchUpResult{}, exhaustedErr
		}
		if exhausted {
			return workspaceObservationCatchUpResult{Skipped: true}, nil
		}
	}

	for attempt := 1; attempt <= workspaceObservationCatchUpMaxAttempts; attempt++ {
		result, err := catchUpWorkspaceObservationsOnce(ctx, db, batchSize)
		result.Retries = attempt - 1
		if err == nil && result.Selected == 0 && stateExists {
			var events int
			if countErr := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM events LIMIT 1)`).Scan(&events); countErr != nil {
				return result, xerrors.Errorf("probe events before marking catch-up exhausted: %w", countErr)
			}
			if events == 1 {
				if markErr := markWorkspaceObservationCatchUpExhausted(ctx, db); markErr != nil {
					return result, markErr
				}
			}
		}
		if err == nil || !isSQLiteBusy(err) || attempt == workspaceObservationCatchUpMaxAttempts {
			return result, err
		}
		timer := time.NewTimer(workspaceObservationCatchUpRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return result, xerrors.Errorf("workspace observation catch-up retry cancelled: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return workspaceObservationCatchUpResult{}, nil
}

func workspaceObservationCatchUpIsExhausted(ctx context.Context, db *sql.DB) (bool, error) {
	var exhausted int
	if err := db.QueryRowContext(ctx, `SELECT exhausted FROM workspace_observation_catchup_state WHERE singleton = 1`).Scan(&exhausted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, xerrors.Errorf("read workspace observation catch-up state: %w", err)
	}
	return exhausted == 1, nil
}

func markWorkspaceObservationCatchUpExhausted(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `UPDATE workspace_observation_catchup_state SET exhausted = 1 WHERE singleton = 1`); err != nil {
		return xerrors.Errorf("mark workspace observation catch-up exhausted: %w", err)
	}
	return nil
}

func catchUpWorkspaceObservationsOnce(ctx context.Context, db *sql.DB, batchSize int) (workspaceObservationCatchUpResult, error) {
	rows, err := db.QueryContext(ctx, workspaceObservationCatchUpBatchQuery, batchSize+1)
	if err != nil {
		return workspaceObservationCatchUpResult{}, xerrors.Errorf("failed to query workspace observation catch-up batch: %w", err)
	}

	type catchUpRow struct {
		eventID, sessionID, workspace, createdAt, agent, sourceHook, canonical string
	}
	batch := make([]catchUpRow, 0, batchSize)
	for rows.Next() {
		var row catchUpRow
		if err := rows.Scan(&row.eventID, &row.sessionID, &row.workspace, &row.createdAt, &row.agent, &row.sourceHook, &row.canonical); err != nil {
			_ = rows.Close()
			return workspaceObservationCatchUpResult{}, xerrors.Errorf("failed to scan workspace observation catch-up row: %w", err)
		}
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return workspaceObservationCatchUpResult{}, xerrors.Errorf("failed to iterate workspace observation catch-up rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return workspaceObservationCatchUpResult{}, xerrors.Errorf("failed to close workspace observation catch-up rows: %w", err)
	}
	result := workspaceObservationCatchUpResult{Selected: len(batch)}
	if len(batch) > batchSize {
		result.MorePending = true
		batch = batch[:batchSize]
		result.Selected = batchSize
	}
	if len(batch) == 0 {
		return result, nil
	}

	// Do not upgrade the candidate-selection snapshot into a writer. A
	// concurrent hook commit between SELECT and INSERT would otherwise turn
	// the upgrade into SQLITE_BUSY_SNAPSHOT even with busy_timeout enabled.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, xerrors.Errorf("failed to begin workspace observation catch-up: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	inserted := 0
	for _, row := range batch {
		relationship := model.ClassifyWorkspaceRelationship(types.Workspace(row.canonical), types.Workspace(row.workspace))
		_, err := tx.ExecContext(ctx, `
			INSERT INTO session_workspace_observations (
				observation_id, session_id, workspace, raw_workspace,
				observation_kind, observation_origin, observed_relationship,
				observed_event_id, delivery_record_id, attribution_fingerprint,
				diagnostic_reason, observed_at, source_client, source_hook
			) VALUES (?, ?, ?, NULL, 'primary', 'backfill', ?, ?, NULL, ?, '', ?, ?, ?)`,
			"backfill:"+row.eventID,
			row.sessionID,
			row.workspace,
			string(relationship),
			row.eventID,
			model.WorkspaceAttributionFingerprint(types.Workspace(row.workspace), ""),
			row.createdAt,
			rootSourceClient(row.agent),
			row.sourceHook,
		)
		if err != nil {
			if isSQLiteUniqueOrPKConflict(err) {
				continue
			}
			return result, xerrors.Errorf("failed to insert backfill workspace observation for event %s: %w", row.eventID, err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return result, xerrors.Errorf("failed to commit workspace observation catch-up: %w", err)
	}
	// Report only durable inserts. An exhausted busy retry may have executed
	// statements in a transaction that was rolled back before commit.
	result.Inserted = inserted
	return result, nil
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlitelib.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code()&0xff == 5
}

// isSQLiteFull reports SQLITE_FULL (13). Catch-up must not treat this as a
// routine incomplete batch (#1836). Match Code() rather than the driver text.
func isSQLiteFull(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlitelib.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code()&0xff == 13
}

func sqliteTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to inspect SQLite table %s: %w", table, err)
}
