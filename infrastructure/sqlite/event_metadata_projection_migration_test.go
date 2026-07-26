package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMigrations_EventMetadataProjectionBackfillsAndMaintainsRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preProjection := onDiskSQLiteMigrationsBefore(t, 34)
	if err := newStoreManagementDatasource(t, dbPath, preProjection).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-projection) error = %v", err)
	}
	db := openProjectionMigrationDB(t, dbPath)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at,
			source_hook, body_original_bytes, body_ingest_truncated,
			body_storage_truncated, body_metadata_version, body_availability
		) VALUES
			('projection-a', 'session_ended', 'hook', 'codex', 'session-a',
			 'workspace-a', '[phase:subagent] complete',
			 '2026-07-26T00:00:00Z', NULL, 128, 0, 0, 1, 'available'),
			('projection-b', 'note', 'cli', 'codex', 'session-b',
			 'workspace-b', 'metadata only',
			 '2026-07-26T00:00:01.5Z', 'stop', 64, 0, 0, 1, 'available');
		INSERT INTO command_audits(
			event_id, command_text, input_text, output_text, exit_code, failed
		) VALUES ('projection-b', 'synthetic', '', '', 9, 1);
	`); err != nil {
		_ = db.Close()
		t.Fatalf("seed pre-projection store: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-projection store: %v", err)
	}

	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(current) error = %v", err)
	}
	db = openProjectionMigrationDB(t, dbPath)
	defer func() { _ = db.Close() }()

	assertMigrationApplied(t, db, 34)
	assertProjectionSchemaIsPayloadFree(t, db)
	assertProjectionRow(t, db, "projection-a", projectionRowExpectation{
		kind:             "session_ended",
		workspace:        "workspace-a",
		createdAtNorm:    "2026-07-26T00:00:00.000000000Z",
		storedBodyBytes:  int64(len("[phase:subagent] complete")),
		legacySourceHook: "subagent_stop",
	})
	assertProjectionRow(t, db, "projection-b", projectionRowExpectation{
		kind:            "note",
		workspace:       "workspace-b",
		createdAtNorm:   "2026-07-26T00:00:01.500000000Z",
		storedBodyBytes: int64(len("metadata only")),
		auditPresent:    true,
		exitCode:        9,
		failed:          true,
	})

	if _, err := db.ExecContext(ctx, `
		UPDATE events
		   SET kind = 'compact_summary',
		       workspace = 'workspace-updated',
		       body = '[phase:pre-compact] complete',
		       created_at = '2026-07-26T00:00:02.25Z',
		       source_hook = NULL
		 WHERE id = 'projection-a';
		UPDATE command_audits
		   SET exit_code = NULL, failed = 0
		 WHERE event_id = 'projection-b';
	`); err != nil {
		t.Fatalf("update authoritative rows: %v", err)
	}
	assertProjectionRow(t, db, "projection-a", projectionRowExpectation{
		kind:             "compact_summary",
		workspace:        "workspace-updated",
		createdAtNorm:    "2026-07-26T00:00:02.250000000Z",
		storedBodyBytes:  int64(len("[phase:pre-compact] complete")),
		legacySourceHook: "pre_compact",
	})
	assertProjectionRow(t, db, "projection-b", projectionRowExpectation{
		kind:            "note",
		workspace:       "workspace-b",
		createdAtNorm:   "2026-07-26T00:00:01.500000000Z",
		storedBodyBytes: int64(len("metadata only")),
		auditPresent:    true,
		failed:          false,
	})

	if _, err := db.ExecContext(ctx, `DELETE FROM command_audits WHERE event_id = 'projection-b'`); err != nil {
		t.Fatalf("delete command audit: %v", err)
	}
	assertProjectionRow(t, db, "projection-b", projectionRowExpectation{
		kind:            "note",
		workspace:       "workspace-b",
		createdAtNorm:   "2026-07-26T00:00:01.500000000Z",
		storedBodyBytes: int64(len("metadata only")),
	})

	if _, err := db.ExecContext(ctx, `DELETE FROM events WHERE id = 'projection-a'`); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM event_metadata_projection WHERE id = 'projection-a'
	`).Scan(&count); err != nil {
		t.Fatalf("count deleted projection row: %v", err)
	}
	if count != 0 {
		t.Fatalf("deleted projection row count = %d, want 0", count)
	}
}

func TestMigrations_EventMetadataProjectionUpgradesOnlyCopiedStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.db")
	copyPath := filepath.Join(root, "copy.db")
	if err := newStoreManagementDatasource(t, sourcePath, onDiskSQLiteMigrationsBefore(t, 34)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(source) error = %v", err)
	}
	source := openProjectionMigrationDB(t, sourcePath)
	if _, err := source.ExecContext(ctx, `
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at,
			body_availability
		) VALUES ('copy-event', 'note', 'cli', 'codex', 'copy-session',
		          'copy-workspace', zeroblob(1048576),
		          '2026-07-26T00:00:00Z', 'unavailable_retention')
	`); err != nil {
		_ = source.Close()
		t.Fatalf("seed copied-store source: %v", err)
	}
	if _, err := source.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = source.Close()
		t.Fatalf("checkpoint source: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}
	before := projectionFileDigest(t, sourcePath)
	copyProjectionFile(t, sourcePath, copyPath)
	sourceInfo := projectionFileInfo(t, sourcePath)
	copyBeforeInfo := projectionFileInfo(t, copyPath)

	started := time.Now()
	if err := newStoreManagementDatasource(t, copyPath, onDiskSQLiteMigrations(t)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(copy) error = %v", err)
	}
	migrationElapsed := time.Since(started)
	after := projectionFileDigest(t, sourcePath)
	if before != after {
		t.Fatal("source store changed while migrating its private copy")
	}

	copyDB := openProjectionMigrationDB(t, copyPath)
	defer func() { _ = copyDB.Close() }()
	var integrity string
	if err := copyDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q, want ok", integrity)
	}
	rows, err := copyDB.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("copied store has a foreign-key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_check: %v", err)
	}
	var eventCount, projectionCount int
	if err := copyDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&eventCount); err != nil {
		t.Fatalf("count copied events: %v", err)
	}
	if err := copyDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_metadata_projection").Scan(&projectionCount); err != nil {
		t.Fatalf("count copied projection: %v", err)
	}
	if eventCount != projectionCount {
		t.Fatalf("copied event/projection counts = %d/%d, want equal", eventCount, projectionCount)
	}
	if _, err := copyDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint copied store: %v", err)
	}
	copyAfterInfo := projectionFileInfo(t, copyPath)
	t.Logf(
		"projection migration metrics: source_bytes=%d copy_before_bytes=%d copy_after_bytes=%d growth_bytes=%d migration_ms=%.3f",
		sourceInfo.Size(),
		copyBeforeInfo.Size(),
		copyAfterInfo.Size(),
		copyAfterInfo.Size()-copyBeforeInfo.Size(),
		float64(migrationElapsed)/float64(time.Millisecond),
	)
}

func TestMigrations_EventMetadataProjectionFailsAtomicallyUnderWriteLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 34)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-projection) error = %v", err)
	}
	locker := openProjectionMigrationDB(t, dbPath)
	defer func() { _ = locker.Close() }()
	if _, err := locker.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("BEGIN IMMEDIATE: %v", err)
	}

	started := time.Now()
	err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).Initialize(ctx)
	elapsed := time.Since(started)
	if err == nil {
		_, _ = locker.ExecContext(ctx, "ROLLBACK")
		t.Fatal("Initialize(current) error = nil under competing write lock")
	}
	lowerError := strings.ToLower(err.Error())
	if !strings.Contains(lowerError, "busy") && !strings.Contains(lowerError, "locked") {
		_, _ = locker.ExecContext(ctx, "ROLLBACK")
		t.Fatalf("Initialize(current) error = %v, want busy/locked classification", err)
	}
	var partialObjects int
	if err := locker.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM sqlite_master
		 WHERE name = 'event_metadata_projection'
		    OR name LIKE 'event_metadata_projection_%'
	`).Scan(&partialObjects); err != nil {
		_, _ = locker.ExecContext(ctx, "ROLLBACK")
		t.Fatalf("inspect partial projection schema: %v", err)
	}
	if partialObjects != 0 {
		_, _ = locker.ExecContext(ctx, "ROLLBACK")
		t.Fatalf("partial projection object count = %d, want 0", partialObjects)
	}
	if _, err := locker.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK competing writer: %v", err)
	}
	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(current) after lock release error = %v", err)
	}
	t.Logf(
		"projection migration lock metrics: busy_observed=true partial_objects=%d wait_ms=%.3f",
		partialObjects,
		float64(elapsed)/float64(time.Millisecond),
	)
}

func TestMigrations_EventMetadataProjectionSupportsPreProjectionWriterRollback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(current) error = %v", err)
	}
	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 34)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-projection writer) error = %v", err)
	}
	db := openProjectionMigrationDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at,
			body_availability
		) VALUES ('rollback-event', 'note', 'cli', 'codex', 'rollback-session',
		          'rollback-workspace', 'synthetic',
		          '2026-07-26T00:00:00Z', 'available')
	`); err != nil {
		t.Fatalf("insert through pre-projection writer: %v", err)
	}
	assertProjectionRow(t, db, "rollback-event", projectionRowExpectation{
		kind:            "note",
		workspace:       "rollback-workspace",
		createdAtNorm:   "2026-07-26T00:00:00.000000000Z",
		storedBodyBytes: int64(len("synthetic")),
	})
}

// BenchmarkEventMetadataProjectionCopiedStoreMigration is opt-in because it
// writes a 256 MiB-class synthetic source plus a private copy. It reports only
// counts, byte extents, booleans, and elapsed time.
func BenchmarkEventMetadataProjectionCopiedStoreMigration(b *testing.B) {
	if os.Getenv("TRACEARY_RUN_METADATA_PROJECTION_MIGRATION_BENCHMARK") != "1" {
		b.Skip("set TRACEARY_RUN_METADATA_PROJECTION_MIGRATION_BENCHMARK=1 to create the copied-store fixture")
	}
	const (
		eventCount = 8
		bodyBytes  = 32 << 20
	)
	b.StopTimer()
	ctx := context.Background()
	root := b.TempDir()
	sourcePath := filepath.Join(root, "source.db")
	copyPath := filepath.Join(root, "copy.db")
	if err := newStoreManagementDatasource(b, sourcePath, onDiskSQLiteMigrationsBefore(b, 34)).Initialize(ctx); err != nil {
		b.Fatalf("Initialize(source) error = %v", err)
	}
	source := openProjectionMigrationDB(b, sourcePath)
	tx, err := source.BeginTx(ctx, nil)
	if err != nil {
		_ = source.Close()
		b.Fatalf("BeginTx(source) error = %v", err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at,
			body_availability
		) VALUES (?, 'note', 'cli', 'codex', 'migration-session',
		          'migration-workspace', zeroblob(?), ?, 'unavailable_retention')
	`)
	if err != nil {
		_ = tx.Rollback()
		_ = source.Close()
		b.Fatalf("prepare synthetic source: %v", err)
	}
	base := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	for index := 0; index < eventCount; index++ {
		if _, err := statement.ExecContext(
			ctx,
			fmt.Sprintf("migration-event-%03d", index),
			bodyBytes,
			base.Add(time.Duration(index)*time.Millisecond).Format(time.RFC3339Nano),
		); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			_ = source.Close()
			b.Fatalf("insert synthetic extent %d: %v", index, err)
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		_ = source.Close()
		b.Fatalf("close synthetic statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		_ = source.Close()
		b.Fatalf("commit synthetic source: %v", err)
	}
	if _, err := source.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = source.Close()
		b.Fatalf("checkpoint synthetic source: %v", err)
	}
	if err := source.Close(); err != nil {
		b.Fatalf("close synthetic source: %v", err)
	}

	sourceDigest := projectionFileDigest(b, sourcePath)
	sourceBytes := projectionFileInfo(b, sourcePath).Size()
	copyProjectionFile(b, sourcePath, copyPath)
	copyBeforeBytes := projectionFileInfo(b, copyPath).Size()
	peakScratchBytes := projectionDirectoryBytes(b, root)
	migrationDone := make(chan error, 1)
	copyStore := newStoreManagementDatasource(b, copyPath, onDiskSQLiteMigrations(b))
	started := time.Now()
	go func() {
		migrationDone <- copyStore.Initialize(ctx)
	}()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	migrationComplete := false
	for !migrationComplete {
		select {
		case err := <-migrationDone:
			if err != nil {
				b.Fatalf("Initialize(copy) error = %v", err)
			}
			migrationComplete = true
		case <-ticker.C:
			currentScratchBytes, err := projectionDirectoryBytesValue(root)
			if err != nil {
				b.Fatalf("measure migration scratch extent: %v", err)
			}
			if currentScratchBytes > peakScratchBytes {
				peakScratchBytes = currentScratchBytes
			}
		}
	}
	migrationElapsed := time.Since(started)
	if currentScratchBytes := projectionDirectoryBytes(b, root); currentScratchBytes > peakScratchBytes {
		peakScratchBytes = currentScratchBytes
	}
	copyDB := openProjectionMigrationDB(b, copyPath)
	var events, projectionRows, migrationApplied int64
	if err := copyDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&events); err != nil {
		_ = copyDB.Close()
		b.Fatalf("count copied events: %v", err)
	}
	if err := copyDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_metadata_projection").Scan(&projectionRows); err != nil {
		_ = copyDB.Close()
		b.Fatalf("count copied projection: %v", err)
	}
	if err := copyDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 34").Scan(&migrationApplied); err != nil {
		_ = copyDB.Close()
		b.Fatalf("inspect projection migration: %v", err)
	}
	var integrity string
	if err := copyDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		_ = copyDB.Close()
		b.Fatalf("integrity_check: %v", err)
	}
	foreignRows, err := copyDB.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		_ = copyDB.Close()
		b.Fatalf("foreign_key_check: %v", err)
	}
	foreignKeyViolations := int64(0)
	for foreignRows.Next() {
		foreignKeyViolations++
	}
	if err := foreignRows.Err(); err != nil {
		_ = foreignRows.Close()
		_ = copyDB.Close()
		b.Fatalf("iterate foreign_key_check: %v", err)
	}
	if err := foreignRows.Close(); err != nil {
		_ = copyDB.Close()
		b.Fatalf("close foreign_key_check: %v", err)
	}
	if _, err := copyDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = copyDB.Close()
		b.Fatalf("checkpoint copied store: %v", err)
	}
	if err := copyDB.Close(); err != nil {
		b.Fatalf("close copied store: %v", err)
	}
	copyAfterBytes := projectionFileInfo(b, copyPath).Size()
	scratchAfterBytes := projectionDirectoryBytes(b, root)
	sourceUnchanged := projectionFileDigest(b, sourcePath) == sourceDigest
	if events != eventCount ||
		projectionRows != events ||
		migrationApplied != 1 ||
		integrity != "ok" ||
		foreignKeyViolations != 0 ||
		!sourceUnchanged {
		b.Fatal("copied-store projection migration failed its safety invariants")
	}
	b.ReportMetric(float64(events), "events")
	b.ReportMetric(float64(projectionRows), "projection_rows")
	b.ReportMetric(float64(sourceBytes), "source_bytes")
	b.ReportMetric(float64(copyBeforeBytes), "copy_before_bytes")
	b.ReportMetric(float64(copyAfterBytes), "copy_after_bytes")
	b.ReportMetric(float64(copyAfterBytes-copyBeforeBytes), "growth_bytes")
	b.ReportMetric(float64(scratchAfterBytes), "scratch_after_bytes")
	b.ReportMetric(float64(peakScratchBytes), "peak_scratch_bytes")
	b.ReportMetric(float64(migrationElapsed)/float64(time.Millisecond), "migration_ms")
	b.ReportMetric(float64(foreignKeyViolations), "foreign_key_violations")
	if integrity == "ok" {
		b.ReportMetric(1, "integrity_ok")
	}
	if sourceUnchanged {
		b.ReportMetric(1, "source_unchanged")
	}
}

func assertProjectionSchemaIsPayloadFree(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(event_metadata_projection)`)
	if err != nil {
		t.Fatalf("inspect projection schema: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			position     int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(
			&position,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			t.Fatalf("scan projection schema: %v", err)
		}
		switch name {
		case "body", "command_text", "input_text", "output_text":
			t.Fatalf("projection contains payload column %q", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate projection schema: %v", err)
	}
}

type projectionRowExpectation struct {
	kind             string
	workspace        string
	createdAtNorm    string
	storedBodyBytes  int64
	legacySourceHook string
	auditPresent     bool
	exitCode         int64
	failed           bool
}

func assertProjectionRow(t *testing.T, db *sql.DB, id string, want projectionRowExpectation) {
	t.Helper()
	var (
		kind, workspace, createdAtNorm string
		storedBodyBytes                int64
		legacySourceHook               sql.NullString
		auditEventID                   sql.NullString
		exitCode                       sql.NullInt64
		failed                         sql.NullBool
	)
	if err := db.QueryRow(`
		SELECT kind, workspace, created_at_norm, body_stored_bytes,
		       legacy_source_hook, command_audit_event_id,
		       command_exit_code, command_failed
		  FROM event_metadata_projection
		 WHERE id = ?
	`, id).Scan(
		&kind,
		&workspace,
		&createdAtNorm,
		&storedBodyBytes,
		&legacySourceHook,
		&auditEventID,
		&exitCode,
		&failed,
	); err != nil {
		t.Fatalf("read projection row: %v", err)
	}
	if kind != want.kind ||
		workspace != want.workspace ||
		createdAtNorm != want.createdAtNorm ||
		storedBodyBytes != want.storedBodyBytes ||
		legacySourceHook.String != want.legacySourceHook ||
		auditEventID.Valid != want.auditPresent ||
		exitCode.Int64 != want.exitCode ||
		failed.Bool != want.failed {
		t.Fatalf(
			"projection row = kind:%q workspace:%q normalized:%q stored:%d legacy:%q audit:%v exit:%d failed:%v",
			kind,
			workspace,
			createdAtNorm,
			storedBodyBytes,
			legacySourceHook.String,
			auditEventID.Valid,
			exitCode.Int64,
			failed.Bool,
		)
	}
}

func openProjectionMigrationDB(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	return db
}

func projectionFileDigest(t testing.TB, path string) [sha256.Size]byte {
	t.Helper()
	file, err := os.Open(path) // #nosec G304 -- test-owned synthetic store
	if err != nil {
		t.Fatalf("open synthetic store for digest: %v", err)
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		t.Fatalf("digest synthetic store: %v", err)
	}
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

func copyProjectionFile(t testing.TB, source, destination string) {
	t.Helper()
	input, err := os.Open(source) // #nosec G304 -- test-owned synthetic store
	if err != nil {
		t.Fatalf("open synthetic source: %v", err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- test-owned synthetic store
	if err != nil {
		t.Fatalf("create synthetic copy: %v", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatalf("copy synthetic store: %v", err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("close synthetic copy: %v", err)
	}
}

func projectionFileInfo(t testing.TB, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat synthetic store: %v", err)
	}
	return info
}

func projectionDirectoryBytes(t testing.TB, root string) int64 {
	t.Helper()
	total, err := projectionDirectoryBytesValue(root)
	if err != nil {
		t.Fatalf("measure synthetic scratch extent: %v", err)
	}
	return total
}

func projectionDirectoryBytesValue(root string) (int64, error) {
	var total int64
	if err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk synthetic scratch: %w", err)
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect synthetic scratch entry: %w", err)
		}
		total += info.Size()
		return nil
	}); err != nil {
		return 0, fmt.Errorf("measure synthetic scratch extent: %w", err)
	}
	return total, nil
}
