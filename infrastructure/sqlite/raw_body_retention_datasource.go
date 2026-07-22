package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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
	sourcePath := d.db.Path()
	db, err := d.db.openAt(ctx, sourcePath)
	if err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to open DB for raw-body plan: %w", err)
	}
	defer closeRawBodyResource(db)

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, xerrors.Errorf("failed to begin raw-body plan snapshot: %w", err)
	}
	defer rollbackRawBodyTx(tx)

	identity, version, err := rawBodySourceIdentity(ctx, tx, sourcePath)
	if err != nil {
		return apptypes.RawBodyRetentionSnapshot{}, err
	}
	migrationDigest, err := rawBodySchemaDigest(ctx, tx)
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
		DatabaseIdentity: identity, SQLiteUserVersion: version, MigrationDigest: migrationDigest, SnapshotAt: time.Now().UTC(),
		Candidates: candidates, ExcludedActive: excluded,
	}, nil
}

// ApplyRawBodyPlan prunes exact candidate versions in durable, resumable batches.
func (d *StoreManagementDatasource) ApplyRawBodyPlan(ctx context.Context, databaseIdentity string, sqliteUserVersion int, migrationDigest, planID string, candidates []apptypes.RawBodyCandidate, appliedAt time.Time) (apptypes.RawBodyApplyResult, error) {
	result := apptypes.RawBodyApplyResult{PlanID: planID, CandidateCount: len(candidates)}
	sourcePath := d.db.Path()
	db, err := d.db.openAt(ctx, sourcePath)
	if err != nil {
		return result, xerrors.Errorf("failed to open DB for raw-body apply: %w", err)
	}
	defer closeRawBodyResource(db)
	stamp := formatTimestamp(appliedAt)
	if err := preflightRawBodyCandidates(ctx, db, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest, planID, candidates); err != nil {
		return result, err
	}
	if err := startRawBodyExecution(ctx, db, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest, planID, len(candidates), stamp); err != nil {
		return result, err
	}
	for index, candidate := range candidates {
		pruned, err := applyRawBodyCandidate(ctx, db, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest, planID, candidate, stamp)
		if err != nil {
			return result, err
		}
		if pruned {
			result.PrunedCount++
		} else {
			result.AlreadyPruned++
		}
		if d.onRawBodyPruned != nil {
			if err := d.onRawBodyPruned(index); err != nil {
				return result, xerrors.Errorf("raw-body apply interrupted after durable batch: %w", err)
			}
		}
	}
	if err := completeRawBodyExecution(ctx, db, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest, planID, len(candidates), stamp); err != nil {
		return result, err
	}
	return result, nil
}

func startRawBodyExecution(ctx context.Context, db *sql.DB, sourcePath, databaseIdentity string, sqliteUserVersion int, migrationDigest, planID string, candidateCount int, stamp string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin raw-body execution: %w", err)
	}
	defer rollbackRawBodyTx(tx)
	if err := requireRawBodySource(ctx, tx, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest); err != nil {
		return err
	}
	var status string
	var recordedCount int
	err = tx.QueryRowContext(ctx, `SELECT status, candidate_count FROM raw_body_retention_executions WHERE plan_id = ?`, planID).Scan(&status, &recordedCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return xerrors.Errorf("failed to inspect raw-body execution: %w", err)
	}
	if err == nil {
		if recordedCount != candidateCount {
			return xerrors.Errorf("raw-body execution candidate count conflicts with plan")
		}
		if status == "restored" {
			return xerrors.Errorf("restored raw-body plan is terminal; create a new plan to prune again")
		}
		if status != "running" && status != "completed" {
			return xerrors.Errorf("raw-body execution cannot resume from status %s", status)
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO raw_body_retention_executions(plan_id, status, candidate_count, pruned_count, started_at)
VALUES (?, 'running', ?, 0, ?)
`, planID, candidateCount, stamp); err != nil {
		return xerrors.Errorf("failed to start raw-body execution: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit raw-body execution start: %w", err)
	}
	return nil
}

func applyRawBodyCandidate(ctx context.Context, db *sql.DB, sourcePath, databaseIdentity string, sqliteUserVersion int, migrationDigest, planID string, candidate apptypes.RawBodyCandidate, stamp string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, xerrors.Errorf("failed to begin raw-body batch: %w", err)
	}
	defer rollbackRawBodyTx(tx)
	if err := requireRawBodySource(ctx, tx, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest); err != nil {
		return false, err
	}
	var executionStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM raw_body_retention_executions WHERE plan_id = ?`, planID).Scan(&executionStatus); err != nil {
		return false, xerrors.Errorf("failed to inspect raw-body execution status: %w", err)
	}
	if executionStatus == "running" {
		res, err := tx.ExecContext(ctx, `UPDATE raw_body_retention_executions SET status = 'running' WHERE plan_id = ? AND status = 'running'`, planID)
		if err != nil {
			return false, xerrors.Errorf("failed to claim raw-body execution batch: %w", err)
		}
		changed, _ := res.RowsAffected()
		if changed != 1 {
			return false, xerrors.Errorf("raw-body execution is no longer running")
		}
	} else if executionStatus != "completed" {
		return false, xerrors.Errorf("raw-body execution cannot apply from status %s", executionStatus)
	}
	body, alreadyPruned, err := verifyRawBodyCandidateState(ctx, tx, planID, candidate)
	if err != nil {
		return false, err
	}
	if alreadyPruned {
		if err := tx.Commit(); err != nil {
			return false, xerrors.Errorf("failed to commit raw-body idempotency check: %w", err)
		}
		return false, nil
	}
	if executionStatus != "running" {
		return false, xerrors.Errorf("completed raw-body execution contains an unpruned candidate %s", candidate.EventID)
	}
	res, err := tx.ExecContext(ctx, `UPDATE events
SET body = ?, body_availability = 'unavailable_retention', body_pruned_at = ?, body_pruned_plan_id = ?
WHERE id = ? AND body_availability = 'available' AND body = ?`, domtypes.EventBodyUnavailableRetentionMarker, stamp, planID, candidate.EventID, body)
	if err != nil {
		return false, xerrors.Errorf("failed to prune raw body %s: %w", candidate.EventID, err)
	}
	changed, _ := res.RowsAffected()
	if changed != 1 {
		return false, xerrors.Errorf("raw-body candidate changed during apply: %s", candidate.EventID)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO raw_body_retention_entries(plan_id, event_id, body_sha256, stored_bytes, pruned_at)
VALUES (?, ?, ?, ?, ?)
`, planID, candidate.EventID, candidate.BodySHA256, candidate.StoredBytes, stamp); err != nil {
		return false, xerrors.Errorf("failed to record raw-body entry: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE raw_body_retention_executions
SET pruned_count = (SELECT COUNT(*) FROM raw_body_retention_entries WHERE plan_id = ?) WHERE plan_id = ?`, planID, planID); err != nil {
		return false, xerrors.Errorf("failed to update raw-body execution progress: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, xerrors.Errorf("failed to commit raw-body batch: %w", err)
	}
	return true, nil
}

func preflightRawBodyCandidates(ctx context.Context, db *sql.DB, sourcePath, databaseIdentity string, sqliteUserVersion int, migrationDigest, planID string, candidates []apptypes.RawBodyCandidate) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return xerrors.Errorf("failed to begin raw-body preflight: %w", err)
	}
	defer rollbackRawBodyTx(tx)
	if err := requireRawBodySource(ctx, tx, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest); err != nil {
		return err
	}
	for _, candidate := range candidates {
		if _, _, err := verifyRawBodyCandidateState(ctx, tx, planID, candidate); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit raw-body preflight: %w", err)
	}
	return nil
}

func verifyRawBodyCandidateState(ctx context.Context, tx *sql.Tx, planID string, candidate apptypes.RawBodyCandidate) (string, bool, error) {
	var body, createdAt, availability string
	var stored int
	var prunedPlan sql.NullString
	var activeSession bool
	err := tx.QueryRowContext(ctx, `SELECT e.body, e.created_at, e.body_availability, e.body_stored_bytes, e.body_pruned_plan_id,
EXISTS(SELECT 1 FROM sessions AS s WHERE s.session_id = e.session_id AND s.ended_at IS NULL)
FROM events AS e WHERE e.id = ?`, candidate.EventID).Scan(&body, &createdAt, &availability, &stored, &prunedPlan, &activeSession)
	if err != nil {
		return "", false, xerrors.Errorf("failed to verify raw-body candidate %s: %w", candidate.EventID, err)
	}
	if availability == domtypes.BodyAvailabilityUnavailableRetention.String() && prunedPlan.String == planID {
		var entryCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM raw_body_retention_entries WHERE plan_id = ? AND event_id = ? AND body_sha256 = ? AND stored_bytes = ?`, planID, candidate.EventID, candidate.BodySHA256, candidate.StoredBytes).Scan(&entryCount); err != nil || entryCount != 1 {
			return "", false, xerrors.Errorf("raw-body execution ledger does not match pruned event %s", candidate.EventID)
		}
		persistedCreatedAt, parseErr := time.Parse(time.RFC3339Nano, createdAt)
		if parseErr != nil || !persistedCreatedAt.Equal(candidate.CreatedAt) || stored != candidate.StoredBytes || body != domtypes.EventBodyUnavailableRetentionMarker || activeSession {
			return "", false, xerrors.Errorf("durable raw-body candidate state changed for event %s", candidate.EventID)
		}
		return body, true, nil
	}
	if activeSession {
		return "", false, xerrors.Errorf("raw-body plan is stale because event %s belongs to an active session", candidate.EventID)
	}
	persistedCreatedAt, parseErr := time.Parse(time.RFC3339Nano, createdAt)
	if parseErr != nil {
		return "", false, xerrors.Errorf("failed to parse raw-body candidate timestamp %s: %w", candidate.EventID, parseErr)
	}
	digest := sha256.Sum256([]byte(body))
	if availability != domtypes.BodyAvailabilityAvailable.String() || !persistedCreatedAt.Equal(candidate.CreatedAt) || stored != candidate.StoredBytes || hex.EncodeToString(digest[:]) != candidate.BodySHA256 {
		return "", false, xerrors.Errorf("raw-body plan is stale or mismatched at event %s", candidate.EventID)
	}
	return body, false, nil
}

func completeRawBodyExecution(ctx context.Context, db *sql.DB, sourcePath, databaseIdentity string, sqliteUserVersion int, migrationDigest, planID string, candidateCount int, stamp string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin raw-body completion: %w", err)
	}
	defer rollbackRawBodyTx(tx)
	if err := requireRawBodySource(ctx, tx, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest); err != nil {
		return err
	}
	var executionStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM raw_body_retention_executions WHERE plan_id = ?`, planID).Scan(&executionStatus); err != nil {
		return xerrors.Errorf("failed to inspect raw-body completion status: %w", err)
	}
	if executionStatus != "running" && executionStatus != "completed" {
		return xerrors.Errorf("raw-body execution cannot complete from status %s", executionStatus)
	}
	var ledgerCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM raw_body_retention_entries WHERE plan_id = ?`, planID).Scan(&ledgerCount); err != nil {
		return xerrors.Errorf("failed to count raw-body execution ledger: %w", err)
	}
	if ledgerCount != candidateCount {
		return xerrors.Errorf("raw-body execution is incomplete: ledger has %d of %d candidates", ledgerCount, candidateCount)
	}
	if executionStatus == "completed" {
		if err := tx.Commit(); err != nil {
			return xerrors.Errorf("failed to commit raw-body completion check: %w", err)
		}
		return nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE raw_body_retention_executions
	SET status = 'completed', pruned_count = ?, completed_at = ? WHERE plan_id = ? AND status = 'running'`, candidateCount, stamp, planID)
	if err != nil {
		return xerrors.Errorf("failed to complete raw-body execution: %w", err)
	}
	changed, _ := res.RowsAffected()
	if changed != 1 {
		return xerrors.Errorf("raw-body execution changed state before completion")
	}
	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit raw-body completion: %w", err)
	}
	return nil
}

// RestoreRawBodyPlan restores exact bodies only for the execution that pruned them.
func (d *StoreManagementDatasource) RestoreRawBodyPlan(ctx context.Context, databaseIdentity string, sqliteUserVersion int, migrationDigest, planID string, bodies []apptypes.RawBodyRecoveryBody, restoredAt time.Time) (apptypes.RawBodyRestoreResult, error) {
	result := apptypes.RawBodyRestoreResult{PlanID: planID, CandidateCount: len(bodies)}
	sourcePath := d.db.Path()
	db, err := d.db.openAt(ctx, sourcePath)
	if err != nil {
		return result, xerrors.Errorf("failed to open DB for raw-body restore: %w", err)
	}
	defer closeRawBodyResource(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, xerrors.Errorf("failed to begin raw-body restore: %w", err)
	}
	defer rollbackRawBodyTx(tx)
	if err := requireRawBodySource(ctx, tx, sourcePath, databaseIdentity, sqliteUserVersion, migrationDigest); err != nil {
		return result, err
	}
	var executionStatus string
	var executionCandidates int
	if err := tx.QueryRowContext(ctx, `SELECT status, candidate_count FROM raw_body_retention_executions WHERE plan_id = ?`, planID).Scan(&executionStatus, &executionCandidates); err != nil {
		return result, xerrors.Errorf("raw-body execution is not available for restore: %w", err)
	}
	if executionCandidates != len(bodies) || (executionStatus != "running" && executionStatus != "completed" && executionStatus != "restored") {
		return result, xerrors.Errorf("raw-body execution state does not match restore plan")
	}
	if executionStatus != "restored" {
		res, err := tx.ExecContext(ctx, `UPDATE raw_body_retention_executions SET status = 'restoring' WHERE plan_id = ? AND status = ?`, planID, executionStatus)
		if err != nil {
			return result, xerrors.Errorf("failed to claim raw-body restore: %w", err)
		}
		changed, _ := res.RowsAffected()
		if changed != 1 {
			return result, xerrors.Errorf("raw-body execution changed state before restore")
		}
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
		var ledgerDigest string
		var ledgerStored int
		var ledgerRestoredAt sql.NullString
		ledgerErr := tx.QueryRowContext(ctx, `SELECT body_sha256, stored_bytes, restored_at FROM raw_body_retention_entries WHERE plan_id = ? AND event_id = ?`, planID, recovery.Candidate.EventID).Scan(&ledgerDigest, &ledgerStored, &ledgerRestoredAt)
		if errors.Is(ledgerErr, sql.ErrNoRows) {
			if executionStatus != "running" || availability != domtypes.BodyAvailabilityAvailable.String() {
				return result, xerrors.Errorf("raw-body restore ledger is missing event %s", recovery.Candidate.EventID)
			}
			// A running execution may have stopped before this candidate. Preserve
			// the currently available body, including legitimate post-plan changes.
			result.AlreadyRestored++
			continue
		}
		if ledgerErr != nil || ledgerDigest != recovery.Candidate.BodySHA256 || ledgerStored != recovery.Candidate.StoredBytes {
			return result, xerrors.Errorf("raw-body restore ledger does not match event %s", recovery.Candidate.EventID)
		}
		if availability == domtypes.BodyAvailabilityAvailable.String() {
			if !ledgerRestoredAt.Valid {
				return result, xerrors.Errorf("event %s is available before its restore was recorded", recovery.Candidate.EventID)
			}
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
}, databasePath string) (string, int, error) {
	var lineageIdentity string
	if err := q.QueryRowContext(ctx, `SELECT id FROM raw_body_retention_store_identity`).Scan(&lineageIdentity); err != nil {
		return "", 0, xerrors.Errorf("failed to read raw-body store identity: %w", err)
	}
	canonicalPath, err := filepath.Abs(databasePath)
	if err != nil {
		return "", 0, xerrors.Errorf("resolve raw-body store path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(canonicalPath); err == nil {
		canonicalPath = resolved
	} else if !os.IsNotExist(err) {
		return "", 0, xerrors.Errorf("resolve raw-body store symlinks: %w", err)
	}
	identityDigest := sha256.Sum256([]byte(lineageIdentity + "\x00" + filepath.Clean(canonicalPath)))
	var version int
	if err := q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return "", 0, xerrors.Errorf("failed to read SQLite user_version: %w", err)
	}
	return hex.EncodeToString(identityDigest[:]), version, nil
}

func rawBodySchemaDigest(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT type, name, tbl_name, sql
  FROM sqlite_schema
 WHERE sql IS NOT NULL
   AND name NOT LIKE 'sqlite_%'
 ORDER BY type, name, tbl_name, sql`)
	if err != nil {
		return "", xerrors.Errorf("failed to read SQLite schema for retention: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hash := sha256.New()
	for rows.Next() {
		var objectType, name, tableName, statement string
		if err := rows.Scan(&objectType, &name, &tableName, &statement); err != nil {
			return "", xerrors.Errorf("failed to scan SQLite schema for retention: %w", err)
		}
		for _, value := range []string{objectType, name, tableName, statement} {
			_, _ = hash.Write([]byte(value))
			_, _ = hash.Write([]byte{0})
		}
	}
	if err := rows.Err(); err != nil {
		return "", xerrors.Errorf("failed to iterate SQLite schema for retention: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func requireRawBodySource(ctx context.Context, tx *sql.Tx, databasePath, expectedIdentity string, expectedVersion int, expectedMigrationDigest string) error {
	identity, version, err := rawBodySourceIdentity(ctx, tx, databasePath)
	if err != nil {
		return err
	}
	migrationDigest, err := rawBodySchemaDigest(ctx, tx)
	if err != nil {
		return err
	}
	if identity != expectedIdentity || version != expectedVersion || migrationDigest != expectedMigrationDigest {
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
