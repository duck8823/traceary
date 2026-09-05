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

	hasEvents, err := tableExists(ctx, db, "events")
	if err != nil {
		return err
	}
	if !hasEvents {
		return nil
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

	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = err
	}
	return nil
}

func dropLegacySearchFamilyOn(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin work-copy search drop: %w", err)
	}
	if err := dropLegacySearchFamily(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit work-copy search drop: %w", err)
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
// sessions on the source.
func (b SQLiteCompactionBuilder) InspectBodyGate(ctx context.Context, source string, cutoff time.Time) (application.BodyGate, error) {
	db, err := openDirectReadOnly(ctx, source)
	if err != nil {
		return application.BodyGate{}, fmt.Errorf("open source for body gate: %w", err)
	}
	defer func() { _ = db.Close() }()
	return inspectBodyGateOn(ctx, db, cutoff)
}

func inspectBodyGateOn(ctx context.Context, db *sql.DB, cutoff time.Time) (application.BodyGate, error) {
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

func duplicatedCommandExecutedBodyPredicate() string {
	return `
kind = 'command_executed'
AND EXISTS (SELECT 1 FROM command_audits a WHERE a.event_id = events.id)
AND length(CAST(body AS BLOB)) > 0`
}

// InspectReclaimableBytes is a bounded metadata-only estimate of bytes
// compact can drop: discardable projection rows older than cutoff plus
// free pages. It does not walk event bodies.
func (SQLiteCompactionBuilder) InspectReclaimableBytes(ctx context.Context, source string, cutoff time.Time) (int64, error) {
	return inspectReclaimableBytes(ctx, source, cutoff)
}

func inspectReclaimableBytes(ctx context.Context, source string, cutoff time.Time) (int64, error) {
	db, err := openDirectReadOnly(ctx, source)
	if err != nil {
		return 0, fmt.Errorf("open source for reclaimable estimate: %w", err)
	}
	defer func() { _ = db.Close() }()
	var pageSize, freePages int64
	if err := db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("read page size for reclaimable estimate: %w", err)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return 0, fmt.Errorf("read freelist for reclaimable estimate: %w", err)
	}
	var discarded int64
	if !cutoff.IsZero() {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='event_metadata_projection'`).Scan(&exists); err != nil {
			return 0, fmt.Errorf("inspect metadata projection for reclaimable estimate: %w", err)
		}
		if exists > 0 {
			if err := db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(body_stored_bytes), 0)
  FROM event_metadata_projection
 WHERE created_at_norm < ?
   AND kind IN ('transcript', 'compact_summary')`, formatTimestamp(cutoff)).Scan(&discarded); err != nil {
				return 0, fmt.Errorf("sum discardable projection bytes: %w", err)
			}
		}
	}
	return discarded + pageSize*freePages, nil
}

// CompactInPlace applies the copy-filter on the leased source and runs
// incremental_vacuum so a dest replica is not required.
func (b SQLiteCompactionBuilder) CompactInPlace(ctx context.Context, source string, filter application.CompactFilter) error {
	if err := runMechanicalCover(ctx, source, filter, false); err != nil {
		return err
	}
	if err := applyCopyFilters(ctx, source, filter); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", directSQLiteRWDSN(source))
	if err != nil {
		return fmt.Errorf("open source for incremental vacuum: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
		return fmt.Errorf("incremental vacuum: %w", err)
	}
	return nil
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
	ready, err := commandBodyReclaimReady(ctx, db)
	if err != nil || !ready {
		return application.CommandBodyReclaim{}, err
	}
	var reclaim application.CommandBodyReclaim
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(length(CAST(body AS BLOB))), 0)
  FROM events
 WHERE `+duplicatedCommandExecutedBodyPredicate()).Scan(&reclaim.Rows, &reclaim.Bytes); err != nil {
		return application.CommandBodyReclaim{}, fmt.Errorf("measure duplicated command_executed bodies: %w", err)
	}
	return reclaim, nil
}

func clearDuplicatedCommandExecutedBodies(ctx context.Context, db *sql.DB) (application.CommandBodyReclaim, error) {
	ready, err := commandBodyReclaimReady(ctx, db)
	if err != nil || !ready {
		return application.CommandBodyReclaim{}, err
	}
	reclaim, err := measureDuplicatedCommandExecutedBodies(ctx, db)
	if err != nil || reclaim.Rows == 0 {
		return reclaim, err
	}
	if _, err := db.ExecContext(ctx, `UPDATE events SET body = ? WHERE `+duplicatedCommandExecutedBodyPredicate(), ""); err != nil {
		return application.CommandBodyReclaim{}, fmt.Errorf("clear duplicated command_executed bodies: %w", err)
	}
	return reclaim, nil
}

func commandBodyReclaimReady(ctx context.Context, db *sql.DB) (bool, error) {
	hasEvents, err := tableExists(ctx, db, "events")
	if err != nil || !hasEvents {
		return false, err
	}
	hasAudits, err := tableExists(ctx, db, "command_audits")
	if err != nil || !hasAudits {
		return false, err
	}
	return true, nil
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
