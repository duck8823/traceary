package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

var (
	_ application.RawBodyRetentionPlanner  = (*StoreManagementDatasource)(nil)
	_ application.RawBodyRetentionExecutor = (*StoreManagementDatasource)(nil)
)

// ListRawBodyCandidates returns exact eligible identities under one read snapshot.
func (d *StoreManagementDatasource) ListRawBodyCandidates(ctx context.Context, before time.Time) (apptypes.RawBodyRetentionSnapshot, error) {
	if before.IsZero() {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("retention cutoff must not be zero")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to open DB for raw-body plan: %w", err)
	}
	defer closeRawBodyResource(db)

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to begin raw-body plan snapshot: %w", err)
	}
	defer rollbackRawBodyTx(tx)

	identity, version, err := rawBodySourceIdentity(ctx, tx)
	if err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT e.id, e.created_at, e.body_stored_bytes, e.body
  FROM events AS e
 WHERE e.body_availability = 'available'
   AND ts_norm(e.created_at) < ts_norm(?)
   AND NOT EXISTS (
       SELECT 1 FROM sessions AS s
        WHERE s.session_id = e.session_id AND s.ended_at IS NULL
   )
 ORDER BY ts_norm(e.created_at), e.id`, formatTimestamp(before))
	if err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to query raw-body candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	candidates := make([]apptypes.RawBodyCandidate, 0)
	for rows.Next() {
		var id, createdAtValue, body string
		var stored sql.NullInt64
		if err := rows.Scan(&id, &createdAtValue, &stored, &body); err != nil {
			return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to scan raw-body candidate: %w", err)
		}
		if !stored.Valid || stored.Int64 < 0 {
			return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("event %s has indeterminate stored body bytes", id)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
		if err != nil {
			return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to parse candidate created_at: %w", err)
		}
		digest := sha256.Sum256([]byte(body))
		storedBytes, err := checkedInt(stored.Int64, "stored body bytes")
		if err != nil {
			return apptypes.RawBodyRetentionSnapshot{}, err
		}
		candidates = append(candidates, apptypes.RawBodyCandidate{
			EventID: id, CreatedAt: createdAt, StoredBytes: storedBytes, BodySHA256: hex.EncodeToString(digest[:]),
		})
	}
	if err := rows.Err(); err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to iterate raw-body candidates: %w", err)
	}

	excludedRows, err := tx.QueryContext(ctx, `
SELECT e.id
  FROM events AS e
  JOIN sessions AS s ON s.session_id = e.session_id
 WHERE e.body_availability = 'available'
   AND ts_norm(e.created_at) < ts_norm(?)
   AND s.ended_at IS NULL
 ORDER BY e.id`, formatTimestamp(before))
	if err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to query active-session exclusions: %w", err)
	}
	defer func() { _ = excludedRows.Close() }()
	excluded := make([]string, 0)
	for excludedRows.Next() {
		var id string
		if err := excludedRows.Scan(&id); err != nil {
			return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to scan active-session exclusion: %w", err)
		}
		excluded = append(excluded, id)
	}
	if err := excludedRows.Err(); err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to iterate active-session exclusions: %w", err)
	}

	return apptypes.RawBodyRetentionSnapshot{
		DatabaseIdentity: identity, SQLiteUserVersion: version, SnapshotAt: time.Now().UTC(),
		Candidates: candidates, ExcludedActive: excluded,
	}, nil
}

// ApplyRawBodyPlan prunes only exact candidate versions in one transaction.
func (d *StoreManagementDatasource) ApplyRawBodyPlan(ctx context.Context, databaseIdentity string, sqliteUserVersion int, planID string, candidates []apptypes.RawBodyCandidate, appliedAt time.Time) (apptypes.RawBodyApplyResult, error) {
	result := apptypes.RawBodyApplyResult{PlanID: planID, CandidateCount: len(candidates)}
	db, err := d.db.open(ctx)
	if err != nil {
		return result, xerrors.Errorf("failed to open DB for raw-body apply: %w", err)
	}
	defer closeRawBodyResource(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, xerrors.Errorf("failed to begin raw-body apply: %w", err)
	}
	defer rollbackRawBodyTx(tx)
	if err := requireRawBodySource(ctx, tx, databaseIdentity, sqliteUserVersion); err != nil {
		return result, err
	}
	var executionStatus string
	var executionCandidates int
	executionErr := tx.QueryRowContext(ctx, `SELECT status, candidate_count FROM raw_body_retention_executions WHERE plan_id = ?`, planID).Scan(&executionStatus, &executionCandidates)
	if executionErr != nil && !errors.Is(executionErr, sql.ErrNoRows) {
		return result, xerrors.Errorf("failed to inspect raw-body execution: %w", executionErr)
	}
	if executionErr == nil {
		if executionCandidates != len(candidates) {
			return result, xerrors.Errorf("raw-body execution candidate count conflicts with plan")
		}
		if executionStatus == "restored" {
			return result, xerrors.Errorf("restored raw-body plan is terminal; create a new plan to prune again")
		}
	}

	type pendingCandidate struct {
		candidate apptypes.RawBodyCandidate
		body      string
	}
	pending := make([]pendingCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		var body, createdAt, availability string
		var stored int
		var prunedPlan sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT body, created_at, body_availability, body_stored_bytes, body_pruned_plan_id FROM events WHERE id = ?`, candidate.EventID).
			Scan(&body, &createdAt, &availability, &stored, &prunedPlan)
		if err != nil {
			return result, xerrors.Errorf("failed to verify raw-body candidate %s: %w", candidate.EventID, err)
		}
		if availability == domtypes.BodyAvailabilityUnavailableRetention.String() && prunedPlan.String == planID {
			var entryCount int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM raw_body_retention_entries WHERE plan_id = ? AND event_id = ? AND body_sha256 = ? AND stored_bytes = ?`, planID, candidate.EventID, candidate.BodySHA256, candidate.StoredBytes).Scan(&entryCount); err != nil || entryCount != 1 {
				return result, xerrors.Errorf("raw-body execution ledger does not match pruned event %s", candidate.EventID)
			}
			result.AlreadyPruned++
			continue
		}
		digest := sha256.Sum256([]byte(body))
		if availability != domtypes.BodyAvailabilityAvailable.String() || createdAt != formatTimestamp(candidate.CreatedAt) || stored != candidate.StoredBytes || hex.EncodeToString(digest[:]) != candidate.BodySHA256 {
			return result, xerrors.Errorf("raw-body plan is stale or mismatched at event %s", candidate.EventID)
		}
		pending = append(pending, pendingCandidate{candidate: candidate, body: body})
	}

	stamp := formatTimestamp(appliedAt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO raw_body_retention_executions(plan_id, status, candidate_count, pruned_count, started_at)
VALUES (?, 'running', ?, 0, ?)
ON CONFLICT(plan_id) DO NOTHING`, planID, len(candidates), stamp); err != nil {
		return result, xerrors.Errorf("failed to start raw-body execution: %w", err)
	}
	for index, item := range pending {
		res, err := tx.ExecContext(ctx, `UPDATE events
SET body = ?, body_availability = 'unavailable_retention', body_pruned_at = ?, body_pruned_plan_id = ?
WHERE id = ? AND body_availability = 'available' AND body = ?`, domtypes.EventBodyUnavailableRetentionMarker, stamp, planID, item.candidate.EventID, item.body)
		if err != nil {
			return result, xerrors.Errorf("failed to prune raw body %s: %w", item.candidate.EventID, err)
		}
		changed, _ := res.RowsAffected()
		if changed != 1 {
			return result, xerrors.Errorf("raw-body candidate changed during apply: %s", item.candidate.EventID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO raw_body_retention_entries(plan_id, event_id, body_sha256, stored_bytes, pruned_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(plan_id, event_id) DO NOTHING`, planID, item.candidate.EventID, item.candidate.BodySHA256, item.candidate.StoredBytes, stamp); err != nil {
			return result, xerrors.Errorf("failed to record raw-body entry: %w", err)
		}
		result.PrunedCount++
		if d.onRawBodyPruned != nil {
			if err := d.onRawBodyPruned(index); err != nil {
				return result, xerrors.Errorf("raw-body apply interrupted: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE raw_body_retention_executions
SET status = 'completed', pruned_count = ?, completed_at = ? WHERE plan_id = ?`, len(candidates), stamp, planID); err != nil {
		return result, xerrors.Errorf("failed to complete raw-body execution: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, xerrors.Errorf("failed to commit raw-body apply: %w", err)
	}
	return result, nil
}

// RestoreRawBodyPlan restores exact bodies only for the execution that pruned them.
func (d *StoreManagementDatasource) RestoreRawBodyPlan(ctx context.Context, databaseIdentity string, sqliteUserVersion int, planID string, bodies []apptypes.RawBodyRecoveryBody, restoredAt time.Time) (apptypes.RawBodyRestoreResult, error) {
	result := apptypes.RawBodyRestoreResult{PlanID: planID, CandidateCount: len(bodies)}
	db, err := d.db.open(ctx)
	if err != nil {
		return result, xerrors.Errorf("failed to open DB for raw-body restore: %w", err)
	}
	defer closeRawBodyResource(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, xerrors.Errorf("failed to begin raw-body restore: %w", err)
	}
	defer rollbackRawBodyTx(tx)
	if err := requireRawBodySource(ctx, tx, databaseIdentity, sqliteUserVersion); err != nil {
		return result, err
	}
	var executionStatus string
	var executionCandidates int
	if err := tx.QueryRowContext(ctx, `SELECT status, candidate_count FROM raw_body_retention_executions WHERE plan_id = ?`, planID).Scan(&executionStatus, &executionCandidates); err != nil {
		return result, xerrors.Errorf("raw-body execution is not available for restore: %w", err)
	}
	if executionCandidates != len(bodies) || (executionStatus != "completed" && executionStatus != "restored") {
		return result, xerrors.Errorf("raw-body execution state does not match restore plan")
	}

	stamp := formatTimestamp(restoredAt)
	for _, recovery := range bodies {
		digest := sha256.Sum256([]byte(recovery.Body))
		if len(recovery.Body) != recovery.Candidate.StoredBytes || hex.EncodeToString(digest[:]) != recovery.Candidate.BodySHA256 {
			return result, xerrors.Errorf("recovery body does not match plan for event %s", recovery.Candidate.EventID)
		}
		var body, availability string
		var prunedPlan sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT body, body_availability, body_pruned_plan_id FROM events WHERE id = ?`, recovery.Candidate.EventID).Scan(&body, &availability, &prunedPlan); err != nil {
			return result, xerrors.Errorf("failed to verify restore event %s: %w", recovery.Candidate.EventID, err)
		}
		if availability == domtypes.BodyAvailabilityAvailable.String() {
			current := sha256.Sum256([]byte(body))
			if hex.EncodeToString(current[:]) != recovery.Candidate.BodySHA256 {
				return result, xerrors.Errorf("available body conflicts with recovery for event %s", recovery.Candidate.EventID)
			}
			result.AlreadyRestored++
			continue
		}
		if availability != domtypes.BodyAvailabilityUnavailableRetention.String() || prunedPlan.String != planID {
			return result, xerrors.Errorf("event %s was not pruned by plan %s", recovery.Candidate.EventID, planID)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET body = ?, body_availability = 'available', body_pruned_at = NULL, body_pruned_plan_id = NULL WHERE id = ?`, recovery.Body, recovery.Candidate.EventID); err != nil {
			return result, xerrors.Errorf("failed to restore raw body %s: %w", recovery.Candidate.EventID, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE raw_body_retention_entries SET restored_at = ? WHERE plan_id = ? AND event_id = ?`, stamp, planID, recovery.Candidate.EventID); err != nil {
			return result, xerrors.Errorf("failed to record raw-body restore: %w", err)
		}
		result.RestoredCount++
	}
	if _, err := tx.ExecContext(ctx, `UPDATE raw_body_retention_executions SET status = 'restored', completed_at = ? WHERE plan_id = ?`, stamp, planID); err != nil {
		return result, xerrors.Errorf("failed to complete raw-body restore: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, xerrors.Errorf("failed to commit raw-body restore: %w", err)
	}
	return result, nil
}

func rawBodySourceIdentity(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, int, error) {
	var identity string
	if err := q.QueryRowContext(ctx, `SELECT id FROM raw_body_retention_store_identity`).Scan(&identity); err != nil {
		return "", 0, xerrors.Errorf("failed to read raw-body store identity: %w", err)
	}
	var version int
	if err := q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return "", 0, xerrors.Errorf("failed to read SQLite user_version: %w", err)
	}
	return identity, version, nil
}

func requireRawBodySource(ctx context.Context, tx *sql.Tx, expectedIdentity string, expectedVersion int) error {
	identity, version, err := rawBodySourceIdentity(ctx, tx)
	if err != nil {
		return err
	}
	if identity != expectedIdentity || version != expectedVersion {
		return xerrors.Errorf("raw-body plan belongs to a different store")
	}
	return nil
}

func closeRawBodyResource(db *sql.DB) {
	if err := db.Close(); err != nil {
		slog.Debug("failed to close raw-body retention resource", "error", err)
	}
}

func rollbackRawBodyTx(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		slog.Debug("failed to rollback raw-body retention transaction", "error", err)
	}
}
