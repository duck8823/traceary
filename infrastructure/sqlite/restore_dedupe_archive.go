package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

const restoreDedupeArchiveTable = "event_content_dedupe_archive"

// restoreDedupeArchivePageRows bounds one restore page in memory and in WAL:
// a page holds at most this many archive rows (each body decodes to at most
// maxDecodedPayloadBytes) and commits in its own transaction followed by a
// WAL TRUNCATE, so a large archive no longer loads every row into the process
// or grows the candidate WAL without limit (#2347).
const restoreDedupeArchivePageRows = 200

// restoreQueryer is satisfied by *sql.DB (autocommit probes) and *sql.Tx
// (page and final transactions) so the restore assertions keep one shape.
type restoreQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

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

	// 081 runs before 082, so the candidate still carries body_codec at
	// restore time. Removing this probe would break codec-era restores.
	hasCodec, err := tableHasColumn(ctx, db, "events", "body_codec")
	if err != nil {
		return err
	}
	var auditCountBefore int64
	if err = countCommandAudits(ctx, db, &auditCountBefore); err != nil {
		return err
	}
	processed, err := restoreDedupeArchivePages(ctx, db, rowCount, hasCodec)
	if err != nil {
		return err
	}
	if processed != rowCount {
		return restoreRefused(rowCount, "archive row count changed during restore", "")
	}
	if err := clearRestoredDedupeArchive(ctx, db, rowCount, auditCountBefore); err != nil {
		return err
	}
	return nil
}

// restoreDedupeArchivePages reinserts the archive in rowid order, one page per
// transaction, checkpointing the candidate WAL after each commit. A page
// failure refuses the run before the archive table is touched, so the final
// clear step only runs on a fully reinserted archive.
func restoreDedupeArchivePages(ctx context.Context, db *sql.DB, rowCount int, hasCodec bool) (int, error) {
	var lastRowID int64
	processed := 0
	for {
		page, last, err := loadDedupeArchivePage(ctx, db, hasCodec, lastRowID)
		if err != nil {
			return processed, err
		}
		if len(page) == 0 {
			return processed, nil
		}
		if err := insertRestoredArchivePage(ctx, db, page, rowCount, hasCodec); err != nil {
			return processed, err
		}
		if err := checkpointMigrationCandidateWAL(ctx, db); err != nil {
			return processed, err
		}
		processed += len(page)
		lastRowID = last
		if len(page) < restoreDedupeArchivePageRows {
			return processed, nil
		}
	}
}

// insertRestoredArchivePage writes one page in a single transaction with the
// same per-row existence, decode, and insert checks the unbatched restore had.
func insertRestoredArchivePage(ctx context.Context, db *sql.DB, page []restoreArchiveRow, rowCount int, hasCodec bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dedupe-archive restore page: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, row := range page {
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dedupe-archive restore page: %w", err)
	}
	committed = true
	return nil
}

// clearRestoredDedupeArchive runs the conservation assertions once, after all
// pages committed, then clears the archive in the same transaction.
func clearRestoredDedupeArchive(ctx context.Context, db *sql.DB, rowCount int, auditCountBefore int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dedupe-archive restore clear: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var remaining int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_content_dedupe_archive`).Scan(&remaining); err != nil {
		return fmt.Errorf("count event_content_dedupe_archive rows: %w", err)
	}
	if remaining != rowCount {
		return restoreRefused(rowCount, "archive row count changed during restore", "")
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
		return fmt.Errorf("commit dedupe-archive restore clear: %w", err)
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

// loadDedupeArchivePage reads one rowid-ordered page past lastRowID. The
// archive table is a plain rowid table, so keyset paging is stable across the
// per-page commits in restoreDedupeArchivePages.
func loadDedupeArchivePage(ctx context.Context, db *sql.DB, hasCodec bool, lastRowID int64) ([]restoreArchiveRow, int64, error) {
	query := `SELECT rowid, id, kind, client, agent, session_id, workspace, created_at, source_hook, body
		   FROM event_content_dedupe_archive WHERE rowid > ? ORDER BY rowid LIMIT ?`
	if hasCodec {
		query = `SELECT rowid, id, kind, client, agent, session_id, workspace, created_at, source_hook, body,
		       body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		   FROM event_content_dedupe_archive WHERE rowid > ? ORDER BY rowid LIMIT ?`
	}
	rows, err := db.QueryContext(ctx, query, lastRowID, restoreDedupeArchivePageRows)
	if err != nil {
		return nil, lastRowID, fmt.Errorf("query event_content_dedupe_archive: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var page []restoreArchiveRow
	last := lastRowID
	for rows.Next() {
		var row restoreArchiveRow
		var rowID int64
		dest := []any{
			&rowID, &row.id, &row.kind, &row.client, &row.agent, &row.sessionID,
			&row.workspace, &row.createdAt, &row.sourceHook, &row.payload.Stored,
		}
		if hasCodec {
			dest = append(dest,
				&row.payload.Codec, &row.payload.FormatVersion, &row.payload.PlaintextBytes,
				&row.payload.StoredBytes, &row.payload.SHA256,
			)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, lastRowID, fmt.Errorf("scan event_content_dedupe_archive row: %w", err)
		}
		page = append(page, row)
		last = rowID
	}
	if err := rows.Err(); err != nil {
		return nil, lastRowID, fmt.Errorf("iterate event_content_dedupe_archive: %w", err)
	}
	return page, last, nil
}

func archiveBodyArg(payload payloadRow) any {
	codec := payload.Codec.String
	if !payload.Codec.Valid {
		codec = payloadCodecIdentity
	}
	return storedBodyArg(encodedPayload{Codec: codec, Bytes: payload.Stored})
}

func insertRestoredArchiveEvent(ctx context.Context, q restoreQueryer, row restoreArchiveRow, hasCodec bool) error {
	query := insertEventQuery
	args := []any{row.id, row.kind, row.client, row.agent, row.sessionID, row.workspace, archiveBodyArg(row.payload), row.createdAt, row.sourceHook}
	if hasCodec {
		query = `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		args = append(args, row.payload.Codec, row.payload.FormatVersion, row.payload.PlaintextBytes, row.payload.StoredBytes, row.payload.SHA256)
	}
	if _, err := q.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("restore event %s: %w", row.id, err)
	}
	return nil
}

func assertForeignKeysClean(ctx context.Context, q restoreQueryer, rowCount int) error {
	rows, err := q.QueryContext(ctx, `PRAGMA foreign_key_check`)
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

func assertRefinementEndpointsResolve(ctx context.Context, q restoreQueryer, rowCount int) error {
	var name string
	err := q.QueryRowContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name='session_refinements'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect session_refinements: %w", err)
	}
	rows, err := q.QueryContext(ctx, `SELECT covers_from_event_id, covers_to_event_id FROM session_refinements`)
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
			switch err := q.QueryRowContext(ctx, `SELECT 1 FROM events WHERE id = ?`, eventID).Scan(&exists); {
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

func countCommandAudits(ctx context.Context, q restoreQueryer, count *int64) error {
	var name string
	err := q.QueryRowContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name='command_audits'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		*count = 0
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect command_audits: %w", err)
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM command_audits`).Scan(count); err != nil {
		return fmt.Errorf("count command_audits: %w", err)
	}
	return nil
}

func restoreRefused(rowCount int, reason, eventID string) error {
	return &apptypes.DedupeArchiveRestoreRefusedError{RowCount: rowCount, Reason: reason, EventID: eventID}
}
