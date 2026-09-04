package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

const restoreDedupeArchiveTable = "event_content_dedupe_archive"

func pendingHasRestoreDedupeArchive(plan PreparedMigrationPlan) bool {
	for _, migration := range plan.Pending {
		if conservationLawFor(migration.Version) == ConservationLawRestoreDedupeArchive {
			return true
		}
	}
	return false
}

func restoreDedupeArchiveOrRefuse(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, restoreDedupeArchiveTable)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	var rowCount int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_content_dedupe_archive`).Scan(&rowCount); err != nil {
		return fmt.Errorf("count event_content_dedupe_archive rows: %w", err)
	}
	if rowCount == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dedupe-archive restore: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	hasCodec, err := transactionColumnExists(ctx, tx, "events", "body_codec")
	if err != nil {
		return err
	}
	archived, err := loadDedupeArchiveRows(ctx, tx, hasCodec)
	if err != nil {
		return err
	}
	if len(archived) != rowCount {
		return restoreRefused(rowCount, "archive row count changed during restore", "")
	}

	var auditCountBefore int64
	if err = countCommandAudits(ctx, tx, &auditCountBefore); err != nil {
		return err
	}

	for _, row := range archived {
		var exists int
		switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM events WHERE id = ?`, row.id).Scan(&exists); {
		case err == nil:
			return restoreRefused(rowCount, "event already exists in events", row.id)
		case errors.Is(err, sql.ErrNoRows):
		default:
			return fmt.Errorf("check existing event %s: %w", row.id, err)
		}
		if _, err := row.payload.decode(maxDecodedPayloadBytes); err != nil {
			return restoreRefused(rowCount, "payload decode failed", row.id)
		}
		if err := insertRestoredArchiveEvent(ctx, tx, row, hasCodec); err != nil {
			return err
		}
	}

	if err := assertForeignKeysClean(ctx, tx, rowCount); err != nil {
		return err
	}
	if err := assertRefinementEndpointsResolve(ctx, tx, rowCount); err != nil {
		return err
	}
	var auditCountAfter int64
	if err = countCommandAudits(ctx, tx, &auditCountAfter); err != nil {
		return err
	}
	if auditCountAfter != auditCountBefore {
		return restoreRefused(rowCount, "command_audits row count changed", "")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM event_content_dedupe_archive`); err != nil {
		return fmt.Errorf("clear restored event_content_dedupe_archive: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dedupe-archive restore: %w", err)
	}
	committed = true
	return nil
}

type restoreArchiveRow struct {
	id         string
	kind       string
	client     string
	agent      string
	sessionID  string
	workspace  string
	createdAt  string
	sourceHook sql.NullString
	payload    payloadRow
}

func loadDedupeArchiveRows(ctx context.Context, tx *sql.Tx, hasCodec bool) ([]restoreArchiveRow, error) {
	query := `SELECT id, kind, client, agent, session_id, workspace, created_at, source_hook, body
		   FROM event_content_dedupe_archive`
	if hasCodec {
		query = `SELECT id, kind, client, agent, session_id, workspace, created_at, source_hook, body,
		       body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		   FROM event_content_dedupe_archive`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query event_content_dedupe_archive: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var archived []restoreArchiveRow
	for rows.Next() {
		var row restoreArchiveRow
		dest := []any{
			&row.id, &row.kind, &row.client, &row.agent, &row.sessionID,
			&row.workspace, &row.createdAt, &row.sourceHook, &row.payload.Stored,
		}
		if hasCodec {
			dest = append(dest,
				&row.payload.Codec, &row.payload.FormatVersion, &row.payload.PlaintextBytes,
				&row.payload.StoredBytes, &row.payload.SHA256,
			)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan event_content_dedupe_archive row: %w", err)
		}
		archived = append(archived, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event_content_dedupe_archive: %w", err)
	}
	return archived, nil
}

func insertRestoredArchiveEvent(ctx context.Context, tx *sql.Tx, row restoreArchiveRow, hasCodec bool) error {
	query := insertEventQuery
	args := []any{row.id, row.kind, row.client, row.agent, row.sessionID, row.workspace, archiveBodyArg(row.payload), row.createdAt, row.sourceHook}
	if hasCodec {
		query = `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		args = append(args, row.payload.Codec, row.payload.FormatVersion, row.payload.PlaintextBytes, row.payload.StoredBytes, row.payload.SHA256)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("restore event %s: %w", row.id, err)
	}
	return nil
}

func assertForeignKeysClean(ctx context.Context, tx *sql.Tx, rowCount int) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		return restoreRefused(rowCount, "PRAGMA foreign_key_check reported violations", "")
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate foreign_key_check: %w", err)
	}
	return nil
}

func assertRefinementEndpointsResolve(ctx context.Context, tx *sql.Tx, rowCount int) error {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name='session_refinements'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect session_refinements: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT covers_from_event_id, covers_to_event_id FROM session_refinements`)
	if err != nil {
		return fmt.Errorf("query session_refinements endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var fromID, toID string
		if err := rows.Scan(&fromID, &toID); err != nil {
			return fmt.Errorf("scan session_refinements endpoint: %w", err)
		}
		for _, eventID := range []string{fromID, toID} {
			var exists int
			switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM events WHERE id = ?`, eventID).Scan(&exists); {
			case err == nil:
			case errors.Is(err, sql.ErrNoRows):
				return restoreRefused(rowCount, "session_refinements coverage endpoint does not resolve", eventID)
			default:
				return fmt.Errorf("resolve refinement endpoint %s: %w", eventID, err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate session_refinements endpoints: %w", err)
	}
	return nil
}

func countCommandAudits(ctx context.Context, tx *sql.Tx, count *int64) error {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name='command_audits'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		*count = 0
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect command_audits: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM command_audits`).Scan(count); err != nil {
		return fmt.Errorf("count command_audits: %w", err)
	}
	return nil
}

func restoreRefused(rowCount int, reason, eventID string) error {
	return &apptypes.DedupeArchiveRestoreRefusedError{RowCount: rowCount, Reason: reason, EventID: eventID}
}
