package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

func copyRegularFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open compaction source for work copy: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create compaction work copy: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy compaction source to work: %w", err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync compaction work copy: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close compaction work copy: %w", err)
	}
	return nil
}

func removeSQLiteSidecars(path string) {
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}

func applyCopyFilters(ctx context.Context, work string, filter application.CompactFilter) error {
	db, err := sql.Open("sqlite", directSQLiteRWDSN(work))
	if err != nil {
		return fmt.Errorf("open compaction work copy: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
		return fmt.Errorf("set work journal_mode: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		// A file that was never in WAL returns an error we can ignore if DELETE
		// already succeeded; keep going when checkpoint is a no-op.
		_ = err
	}

	if err := dropLegacySearchFamilyOn(ctx, db); err != nil {
		return err
	}
	if err := dropUnreadRecentFTSOn(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS event_content_dedupe_archive`); err != nil {
		return fmt.Errorf("drop dedupe archive on work copy: %w", err)
	}

	hasEvents, err := tableExists(ctx, db, "events")
	if err != nil {
		return err
	}
	if !hasEvents {
		return nil
	}

	if err := deleteNonCanonicalDuplicateEvents(ctx, db); err != nil {
		return err
	}

	if _, err := clearDuplicatedCommandExecutedBodies(ctx, db); err != nil {
		return err
	}

	if !filter.Cutoff.IsZero() {
		tx, txErr := db.BeginTx(ctx, nil)
		if txErr != nil {
			return fmt.Errorf("begin work-copy gc: %w", txErr)
		}
		discardedAt := formatTimestamp(time.Now().UTC())
		if _, err := (&StoreManagementDatasource{}).collectGarbageInTx(ctx, tx, filter.Cutoff, apptypes.GarbageCollectionTargetAll, discardedAt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply work-copy discard: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit work-copy discard: %w", err)
		}
	}

	if err := encodeAvailableEventBodies(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = err
	}
	return nil
}

func dropUnreadRecentFTSOn(ctx context.Context, db *sql.DB) error {
	// DROP the unread recent FTS on the work copy only (#1842). Implicit
	// open never does this: the table can be multi-GiB.
	if _, err := db.ExecContext(ctx, `DROP TRIGGER IF EXISTS search_projection_recent_ai`); err != nil {
		return fmt.Errorf("drop recent FTS insert trigger on work copy: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER IF EXISTS search_projection_recent_ad`); err != nil {
		return fmt.Errorf("drop recent FTS delete trigger on work copy: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS search_projection_recent_fts`); err != nil {
		return fmt.Errorf("drop unread recent FTS on work copy: %w", err)
	}
	return nil
}

func dropLegacySearchFamilyOn(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin work-copy search drop: %w", err)
	}
	if err := dropLegacySearchProjection(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit work-copy search drop: %w", err)
	}
	return nil
}

func deleteNonCanonicalDuplicateEvents(ctx context.Context, db *sql.DB) error {
	datasource := &StoreManagementDatasource{}
	survey, err := datasource.identifyDedupeGroups(ctx, db, "", 0)
	if err != nil {
		return fmt.Errorf("identify work-copy duplicates: %w", err)
	}
	plan := planContentEventDedupe(survey, false)
	if len(plan.groups) == 0 {
		return nil
	}
	params := apptypes.ContentEventDedupeParams{
		Apply: true,
		RunID: "compact-copy-filter",
		Now:   time.Now().UTC(),
	}
	if err := datasource.applyDedupeGroups(ctx, db, plan, params); err != nil {
		return fmt.Errorf("delete work-copy duplicates: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS event_content_dedupe_archive`); err != nil {
		return fmt.Errorf("drop work-copy dedupe archive after apply: %w", err)
	}
	return nil
}

func encodeAvailableEventBodies(ctx context.Context, db *sql.DB) error {
	hasCodec, err := databaseColumnExists(ctx, db, "events", "body_codec")
	if err != nil {
		return err
	}
	if !hasCodec {
		return nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, body, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		  FROM events
		 WHERE body_availability = 'available'
		   AND body IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("select bodies to encode: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type update struct {
		id      string
		encoded encodedPayload
	}
	var updates []update
	for rows.Next() {
		var id string
		var row payloadRow
		if err := rows.Scan(append([]any{&id}, row.scanDestinations()...)...); err != nil {
			return fmt.Errorf("scan body to encode: %w", err)
		}
		plain, err := row.decode(maxDecodedPayloadBytes)
		if err != nil {
			return fmt.Errorf("decode body %s for encode: %w", id, err)
		}
		encoded, err := encodeCanonicalPayload(plain, true)
		if err != nil {
			return fmt.Errorf("encode body %s: %w", id, err)
		}
		if row.Codec.Valid && row.Codec.String == encoded.Codec && row.SHA256.Valid && row.SHA256.String == encoded.SHA256 && int64(len(row.Stored)) == encoded.StoredBytes {
			continue
		}
		updates = append(updates, update{id: id, encoded: encoded})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate bodies to encode: %w", err)
	}
	for _, item := range updates {
		if _, err := db.ExecContext(ctx, `
			UPDATE events
			   SET body = ?,
			       body_codec = ?,
			       body_format_version = ?,
			       body_plaintext_bytes = ?,
			       body_encoded_bytes = ?,
			       body_sha256 = ?
			 WHERE id = ?`,
			item.encoded.Bytes,
			item.encoded.Codec,
			item.encoded.FormatVersion,
			item.encoded.PlaintextBytes,
			item.encoded.StoredBytes,
			item.encoded.SHA256,
			item.id,
		); err != nil {
			return fmt.Errorf("write encoded body %s: %w", item.id, err)
		}
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)`, name).Scan(&exists); err != nil {
		return false, fmt.Errorf("inspect table %s: %w", name, err)
	}
	return exists != 0, nil
}

// InspectBodyGate counts discardable-age covered bodies versus unrefined
// sessions on the source. Compact refuses only when covered is empty.
func (b SQLiteCompactionBuilder) InspectBodyGate(ctx context.Context, source string, cutoff time.Time) (application.BodyGate, error) {
	db, err := openDirectReadOnly(ctx, source)
	if err != nil {
		return application.BodyGate{}, fmt.Errorf("open source for body gate: %w", err)
	}
	defer func() { _ = db.Close() }()
	hasEvents, err := tableExists(ctx, db, "events")
	if err != nil {
		return application.BodyGate{}, err
	}
	if !hasEvents {
		return application.BodyGate{}, nil
	}
	supported, err := discardPredicateSupported(ctx, db)
	if err != nil || !supported {
		return application.BodyGate{}, err
	}
	countQuery, err := composeDiscardableEventBodiesQuery(`
SELECT COUNT(*), COALESCE(SUM(length(CAST(body AS BLOB))), 0)
  FROM events
 WHERE id IN (
-- discardable-event-bodies
)`)
	if err != nil {
		return application.BodyGate{}, err
	}
	var gate application.BodyGate
	before := formatTimestamp(cutoff.UTC())
	if err := db.QueryRowContext(ctx, countQuery, before).Scan(&gate.CoveredCount, &gate.CoveredBytes); err != nil {
		return application.BodyGate{}, fmt.Errorf("count covered discardable bodies: %w", err)
	}
	if err := db.QueryRowContext(ctx, unrefinedDiscardableSessionsQuery, before).Scan(&gate.UnrefinedSessions, &gate.UnrefinedBytes); err != nil {
		return application.BodyGate{}, fmt.Errorf("count unrefined discardable sessions: %w", err)
	}
	return gate, nil
}

func duplicatedCommandExecutedBodyPredicate(hasCodec bool) string {
	predicate := `
kind = 'command_executed'
AND EXISTS (SELECT 1 FROM command_audits a WHERE a.event_id = events.id)
AND (length(CAST(body AS BLOB)) > 0`
	if hasCodec {
		predicate += `
	OR COALESCE(body_plaintext_bytes, 0) > 0
	OR COALESCE(body_encoded_bytes, 0) > 0`
	}
	return predicate + `)`
}

// InspectCommandBodyReclaim measures stored bytes of duplicated
// command_executed bodies on the source. Compact reports this sum.
func (SQLiteCompactionBuilder) InspectCommandBodyReclaim(ctx context.Context, source string) (application.CommandBodyReclaim, error) {
	db, err := openDirectReadOnly(ctx, source)
	if err != nil {
		return application.CommandBodyReclaim{}, fmt.Errorf("open source for command body reclaim: %w", err)
	}
	defer func() { _ = db.Close() }()
	return measureDuplicatedCommandExecutedBodies(ctx, db)
}

func measureDuplicatedCommandExecutedBodies(ctx context.Context, db *sql.DB) (application.CommandBodyReclaim, error) {
	ready, hasCodec, err := commandBodyReclaimReady(ctx, db)
	if err != nil || !ready {
		return application.CommandBodyReclaim{}, err
	}
	var reclaim application.CommandBodyReclaim
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(length(CAST(body AS BLOB))), 0)
  FROM events
 WHERE `+duplicatedCommandExecutedBodyPredicate(hasCodec)).Scan(&reclaim.Rows, &reclaim.Bytes); err != nil {
		return application.CommandBodyReclaim{}, fmt.Errorf("measure duplicated command_executed bodies: %w", err)
	}
	return reclaim, nil
}

func clearDuplicatedCommandExecutedBodies(ctx context.Context, db *sql.DB) (application.CommandBodyReclaim, error) {
	ready, hasCodec, err := commandBodyReclaimReady(ctx, db)
	if err != nil || !ready {
		return application.CommandBodyReclaim{}, err
	}
	reclaim, err := measureDuplicatedCommandExecutedBodies(ctx, db)
	if err != nil || reclaim.Rows == 0 {
		return reclaim, err
	}
	empty, err := encodePayload(nil, payloadCodecIdentity)
	if err != nil {
		return application.CommandBodyReclaim{}, fmt.Errorf("encode empty command_executed body: %w", err)
	}
	query := `UPDATE events SET body = ? WHERE ` + duplicatedCommandExecutedBodyPredicate(hasCodec)
	args := []any{storedBodyArg(empty)}
	if hasCodec {
		query = `
UPDATE events
   SET body = ?,
       body_codec = ?,
       body_format_version = ?,
       body_plaintext_bytes = ?,
       body_encoded_bytes = ?,
       body_sha256 = ?
 WHERE ` + duplicatedCommandExecutedBodyPredicate(hasCodec)
		args = []any{
			storedBodyArg(empty),
			empty.Codec,
			empty.FormatVersion,
			empty.PlaintextBytes,
			empty.StoredBytes,
			empty.SHA256,
		}
	}
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return application.CommandBodyReclaim{}, fmt.Errorf("clear duplicated command_executed bodies: %w", err)
	}
	return reclaim, nil
}

func commandBodyReclaimReady(ctx context.Context, db *sql.DB) (bool, bool, error) {
	hasEvents, err := tableExists(ctx, db, "events")
	if err != nil || !hasEvents {
		return false, false, err
	}
	hasAudits, err := tableExists(ctx, db, "command_audits")
	if err != nil || !hasAudits {
		return false, false, err
	}
	hasCodec, err := databaseColumnExists(ctx, db, "events", "body_codec")
	if err != nil {
		return false, false, err
	}
	return true, hasCodec, nil
}

const unrefinedDiscardableSessionsQuery = `
SELECT COUNT(DISTINCT e.session_id), COALESCE(SUM(length(CAST(e.body AS BLOB))), 0)
  FROM events AS e
  JOIN sessions AS s
    ON s.session_id = e.session_id
   AND s.ended_at IS NOT NULL
 WHERE e.kind = 'transcript'
   AND e.body_availability = 'available'
   AND e.session_id IS NOT NULL
   AND ts_valid(e.created_at)
   AND ts_norm(e.created_at) < ts_norm(?)
   AND NOT EXISTS (
       SELECT 1
         FROM session_refinements AS r
         JOIN events AS lo
           ON lo.id = r.covers_from_event_id
          AND lo.session_id = r.session_id
         JOIN events AS hi
           ON hi.id = r.covers_to_event_id
          AND hi.session_id = r.session_id
        WHERE r.session_id = e.session_id
          AND r.covers_from_event_id IS NOT NULL
          AND r.covers_to_event_id IS NOT NULL
          AND ts_valid(lo.created_at)
          AND ts_valid(hi.created_at)
          AND (ts_norm(lo.created_at), lo.id) <= (ts_norm(e.created_at), e.id)
          AND (ts_norm(e.created_at), e.id) <= (ts_norm(hi.created_at), hi.id)
   )
`
