package sqlite

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

const epochZeroHookUsageRepairBatchSize = 1000

const epochZeroHookUsageObservedAt = "1970-01-01T00:00:00.000000000Z"

const epochZeroHookUsageSourceFilter = `
    source_name IN ('stop_hook', 'session_end_hook', 'after_agent_hook')`

const selectEpochZeroHookUsageBatch = `
SELECT observation.observation_id,
       observation.session_id,
       observation.finalized_at,
       COALESCE(
           (SELECT event.created_at
              FROM events AS event
             WHERE event.session_id = observation.session_id
               AND event.kind IN ('session_ended', 'session_started')
             ORDER BY ts_norm(event.created_at) DESC, event.id DESC
             LIMIT 1),
           (SELECT event.created_at
              FROM events AS event
             WHERE event.session_id = observation.session_id
             ORDER BY ts_norm(event.created_at) DESC, event.id DESC
             LIMIT 1),
           (SELECT session.ended_at
              FROM sessions AS session
             WHERE session.session_id = observation.session_id
               AND session.ended_at IS NOT NULL
               AND length(trim(session.ended_at)) > 0),
           (SELECT session.started_at
              FROM sessions AS session
             WHERE session.session_id = observation.session_id)
       )
  FROM usage_observations AS observation
 WHERE ts_norm(observation.observed_at) = ?
   AND ` + epochZeroHookUsageSourceFilter + `
 ORDER BY observation.observation_id
 LIMIT ?`

const updateEpochZeroHookUsageTimes = `
UPDATE usage_observations
   SET observed_at = ?,
       finalized_at = CASE
           WHEN finalized_at IS NOT NULL
            AND ts_norm(finalized_at) = ?
           THEN ?
           ELSE finalized_at
       END
 WHERE observation_id = ?
   AND ts_norm(observed_at) = ?`

// usageObservationsRejectDescriptorUpdateSQL is the production trigger from
// migration 000027. The repair drops it only inside one transaction so
// observed_at can be rewritten for epoch-zero hook rows.
const usageObservationsRejectDescriptorUpdateSQL = `
CREATE TRIGGER usage_observations_reject_descriptor_update
BEFORE UPDATE OF observation_id, session_id, host, source_name, source_version,
    provider, model, scope, accounting, observed_at, snapshot_series,
    snapshot_revision, supersedes_id
ON usage_observations
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'usage observation descriptor is immutable');
END;`

// EpochZeroHookUsageRepairResult is the bounded outcome of one repair pass.
type EpochZeroHookUsageRepairResult struct {
	Selected    int
	Repaired    int
	Skipped     int
	MorePending bool
}

// RepairEpochZeroHookUsageObservations backfills a bounded batch of
// stop-hook-family rows whose observed_at is Unix epoch.
func RepairEpochZeroHookUsageObservations(ctx context.Context, db *sql.DB, batchSize int) (EpochZeroHookUsageRepairResult, error) {
	if batchSize <= 0 {
		batchSize = epochZeroHookUsageRepairBatchSize
	}
	return catchUpEpochZeroHookUsage(ctx, db, batchSize)
}

func catchUpEpochZeroHookUsage(ctx context.Context, db *sql.DB, batchSize int) (EpochZeroHookUsageRepairResult, error) {
	if batchSize <= 0 {
		return EpochZeroHookUsageRepairResult{}, xerrors.Errorf("epoch-zero hook usage repair batch size must be positive")
	}
	exists, err := sqliteTableExists(ctx, db, "usage_observations")
	if err != nil {
		return EpochZeroHookUsageRepairResult{}, err
	}
	if !exists {
		return EpochZeroHookUsageRepairResult{}, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return EpochZeroHookUsageRepairResult{}, xerrors.Errorf("failed to begin epoch-zero hook usage repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS usage_observations_reject_descriptor_update`); err != nil {
		return EpochZeroHookUsageRepairResult{}, xerrors.Errorf("failed to drop usage descriptor trigger for repair: %w", err)
	}

	rows, err := tx.QueryContext(ctx, selectEpochZeroHookUsageBatch, epochZeroHookUsageObservedAt, batchSize+1)
	if err != nil {
		return EpochZeroHookUsageRepairResult{}, xerrors.Errorf("failed to query epoch-zero hook usage observations: %w", err)
	}

	type repairRow struct {
		observationID string
		sessionID     string
		finalizedAt   sql.NullString
		repairAt      sql.NullString
	}
	batch := make([]repairRow, 0, batchSize)
	for rows.Next() {
		var row repairRow
		if err := rows.Scan(&row.observationID, &row.sessionID, &row.finalizedAt, &row.repairAt); err != nil {
			_ = rows.Close()
			return EpochZeroHookUsageRepairResult{}, xerrors.Errorf("failed to scan epoch-zero hook usage observation: %w", err)
		}
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return EpochZeroHookUsageRepairResult{}, xerrors.Errorf("failed to iterate epoch-zero hook usage observations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return EpochZeroHookUsageRepairResult{}, xerrors.Errorf("failed to close epoch-zero hook usage rows: %w", err)
	}

	result := EpochZeroHookUsageRepairResult{Selected: len(batch)}
	if len(batch) > batchSize {
		result.MorePending = true
		batch = batch[:batchSize]
		result.Selected = batchSize
	}

	for _, row := range batch {
		repairedAt, ok := usableSessionRepairTime(row.repairAt.String)
		if !ok {
			result.Skipped++
			continue
		}
		stamp := formatTimestamp(repairedAt)
		if _, err := tx.ExecContext(
			ctx,
			updateEpochZeroHookUsageTimes,
			stamp,
			epochZeroHookUsageObservedAt,
			stamp,
			row.observationID,
			epochZeroHookUsageObservedAt,
		); err != nil {
			return result, xerrors.Errorf("failed to backfill epoch-zero hook usage %s: %w", row.observationID, err)
		}
		result.Repaired++
	}

	if _, err := tx.ExecContext(ctx, usageObservationsRejectDescriptorUpdateSQL); err != nil {
		return result, xerrors.Errorf("failed to restore usage descriptor trigger after repair: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, xerrors.Errorf("failed to commit epoch-zero hook usage repair: %w", err)
	}
	return result, nil
}

func usableSessionRepairTime(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil || parsed.Unix() <= 0 {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func logEpochZeroHookUsageRepair(result EpochZeroHookUsageRepairResult, err error) {
	if err != nil {
		slog.Error("epoch-zero hook usage repair incomplete; retrying on next initialization",
			"selected", result.Selected,
			"repaired", result.Repaired,
			"skipped", result.Skipped,
			"more_pending", result.MorePending,
			"error", err,
		)
		return
	}
	if result.Selected == 0 {
		return
	}
	slog.Info("repaired epoch-zero hook usage observation timestamps",
		"selected", result.Selected,
		"repaired", result.Repaired,
		"skipped", result.Skipped,
		"more_pending", result.MorePending,
	)
}
