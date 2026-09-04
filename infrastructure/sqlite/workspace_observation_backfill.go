package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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

const workspaceObservationCatchUpHeadQuery = `
	SELECT e.id, e.session_id, e.workspace, e.created_at, e.created_at_norm, e.agent,
	       COALESCE(e.source_hook, ''), COALESCE(s.workspace, '')
	  FROM events e
	  LEFT JOIN sessions s ON s.session_id = e.session_id
	 ORDER BY e.created_at_norm DESC, e.id DESC
	 LIMIT ?`

const workspaceObservationCatchUpFrontierQuery = `
	SELECT e.id, e.session_id, e.workspace, e.created_at, e.created_at_norm, e.agent,
	       COALESCE(e.source_hook, ''), COALESCE(s.workspace, '')
	  FROM events e
	  LEFT JOIN sessions s ON s.session_id = e.session_id
	 WHERE (e.created_at_norm, e.id) < (?, ?)
	 ORDER BY e.created_at_norm DESC, e.id DESC
	 LIMIT ?`

// workspaceObservationCatchUpBatchQuery is the frontier form used by the
// EXPLAIN QUERY PLAN test. Head scans (frontier ”) omit the tuple predicate
// so the planner can use idx_events_created_at_norm_id_desc as a backward walk.
const workspaceObservationCatchUpBatchQuery = workspaceObservationCatchUpFrontierQuery

func backfillWorkspaceObservationsBatch(ctx context.Context, db *sql.DB, batchSize int) (workspaceObservationCatchUpResult, error) {
	if batchSize <= 0 {
		return workspaceObservationCatchUpResult{}, xerrors.Errorf("workspace observation backfill batch size must be positive")
	}
	exhausted, err := workspaceObservationBackfillIsExhausted(ctx, db)
	if err != nil {
		return workspaceObservationCatchUpResult{}, err
	}
	if exhausted {
		return workspaceObservationCatchUpResult{Skipped: true}, nil
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

	for attempt := 1; attempt <= workspaceObservationCatchUpMaxAttempts; attempt++ {
		result, err := backfillWorkspaceObservationsOnce(ctx, db, batchSize)
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
			return result, xerrors.Errorf("workspace observation backfill retry cancelled: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return workspaceObservationCatchUpResult{}, nil
}

// workspaceObservationBackfillIsExhausted is the open-path gate: one PK
// point-select on workspace_observation_catchup_state. Missing table or
// missing row means the sweep is not exhausted.
func workspaceObservationBackfillIsExhausted(ctx context.Context, db *sql.DB) (bool, error) {
	var exhausted int
	err := db.QueryRowContext(ctx, `SELECT exhausted FROM workspace_observation_catchup_state WHERE singleton = 1`).Scan(&exhausted)
	if err == nil {
		return exhausted == 1, nil
	}
	if errors.Is(err, sql.ErrNoRows) || sqliteNoSuchTable(err) {
		return false, nil
	}
	return false, xerrors.Errorf("read workspace observation backfill exhausted flag: %w", err)
}

func sqliteNoSuchTable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table")
}

func readWorkspaceObservationCatchUpState(ctx context.Context, db *sql.DB) (bool, string, string, error) {
	var exhausted int
	frontierNorm, frontierID := "", ""
	hasFrontier, err := sqliteColumnExists(ctx, db, workspaceObservationCatchUpStateTable, "frontier_created_at_norm")
	if err != nil {
		return false, "", "", err
	}
	if hasFrontier {
		err = db.QueryRowContext(
			ctx,
			`SELECT exhausted, frontier_created_at_norm, frontier_event_id
			   FROM workspace_observation_catchup_state WHERE singleton = 1`,
		).Scan(&exhausted, &frontierNorm, &frontierID)
	} else {
		err = db.QueryRowContext(ctx, `SELECT exhausted FROM workspace_observation_catchup_state WHERE singleton = 1`).Scan(&exhausted)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", "", nil
		}
		return false, "", "", xerrors.Errorf("read workspace observation catch-up state: %w", err)
	}
	return exhausted == 1, frontierNorm, frontierID, nil
}

func sqliteColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var exists int
	if err := db.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name = ?)`,
		table,
		column,
	).Scan(&exists); err != nil {
		return false, xerrors.Errorf("failed to inspect SQLite column %s.%s: %w", table, column, err)
	}
	return exists == 1, nil
}

func markWorkspaceObservationCatchUpExhausted(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `UPDATE workspace_observation_catchup_state SET exhausted = 1 WHERE singleton = 1`); err != nil {
		return xerrors.Errorf("mark workspace observation catch-up exhausted: %w", err)
	}
	return nil
}

func backfillWorkspaceObservationsOnce(ctx context.Context, db *sql.DB, batchSize int) (workspaceObservationCatchUpResult, error) {
	frontierNorm, frontierID := "", ""
	stateExists, err := sqliteTableExists(ctx, db, workspaceObservationCatchUpStateTable)
	if err != nil {
		return workspaceObservationCatchUpResult{}, err
	}
	hasFrontier := false
	if stateExists {
		_, frontierNorm, frontierID, err = readWorkspaceObservationCatchUpState(ctx, db)
		if err != nil {
			return workspaceObservationCatchUpResult{}, err
		}
		hasFrontier, err = sqliteColumnExists(ctx, db, workspaceObservationCatchUpStateTable, "frontier_created_at_norm")
		if err != nil {
			return workspaceObservationCatchUpResult{}, err
		}
	}

	var rows *sql.Rows
	if frontierNorm == "" && frontierID == "" {
		rows, err = db.QueryContext(ctx, workspaceObservationCatchUpHeadQuery, batchSize+1)
	} else {
		rows, err = db.QueryContext(ctx, workspaceObservationCatchUpFrontierQuery, frontierNorm, frontierID, batchSize+1)
	}
	if err != nil {
		return workspaceObservationCatchUpResult{}, xerrors.Errorf("failed to query workspace observation catch-up batch: %w", err)
	}

	type backfillRow struct {
		eventID, sessionID, workspace, createdAt, createdAtNorm, agent, sourceHook, canonical string
	}
	batch := make([]backfillRow, 0, batchSize)
	for rows.Next() {
		var row backfillRow
		if err := rows.Scan(&row.eventID, &row.sessionID, &row.workspace, &row.createdAt, &row.createdAtNorm, &row.agent, &row.sourceHook, &row.canonical); err != nil {
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
		affected, err := upsertWorkspaceObservation(
			ctx,
			tx,
			row.sessionID,
			row.workspace,
			string(relationship),
			rootSourceClient(row.agent),
			row.sourceHook,
			"primary",
			row.createdAt,
			row.eventID,
			"",
			"",
			model.WorkspaceAttributionFingerprint(types.Workspace(row.workspace), ""),
			"",
			"backfill",
		)
		if err != nil {
			return result, xerrors.Errorf("failed to insert backfill workspace observation for event %s: %w", row.eventID, err)
		}
		if affected == 1 {
			inserted++
		}
	}

	if stateExists && hasFrontier {
		last := batch[len(batch)-1]
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE workspace_observation_catchup_state
			    SET frontier_created_at_norm = ?, frontier_event_id = ?
			  WHERE singleton = 1`,
			last.createdAtNorm,
			last.eventID,
		); err != nil {
			return result, xerrors.Errorf("failed to advance workspace observation catch-up frontier: %w", err)
		}
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
