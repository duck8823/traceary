package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	"github.com/duck8823/traceary/application"
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

func applyCopyFilters(ctx context.Context, work string, _ application.CompactFilter) error {
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

func duplicatedCommandExecutedBodyPredicate() string {
	return `
kind = 'command_executed'
AND EXISTS (SELECT 1 FROM command_audits a WHERE a.event_id = events.id)
AND length(CAST(body AS BLOB)) > 0`
}

// InspectReclaimableBytes is a bounded metadata-only estimate of bytes
// compact can drop: currently free pages. It does not walk event bodies.
func (SQLiteCompactionBuilder) InspectReclaimableBytes(ctx context.Context, source string) (int64, error) {
	return inspectReclaimableBytes(ctx, source)
}

func inspectReclaimableBytes(ctx context.Context, source string) (int64, error) {
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
	return pageSize * freePages, nil
}

// CompactInPlace applies the copy-filter on the leased source and runs
// incremental_vacuum so a dest replica is not required.
func (b SQLiteCompactionBuilder) CompactInPlace(ctx context.Context, source string, filter application.CompactFilter) error {
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
