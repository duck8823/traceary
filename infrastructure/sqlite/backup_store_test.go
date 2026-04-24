package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_CreateBackup(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	db := sqlite.NewDatabase(dbPath, backupTestMigrations())
	eventDS := sqlite.NewEventDatasource(db)
	storeManager := sqlite.NewStoreManagementDatasource(db)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	event := newEventForSQLiteTest(
		t,
		"event-1",
		"cli",
		"codex",
		"session-1",
		"duck8823/traceary",
		"hello",
		time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC),
	)
	if err := eventDS.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "backup", "traceary-backup.db")
	if err := storeManager.CreateBackup(context.Background(), outputPath, false); err != nil {
		t.Fatalf("CreateBackup() error = %v", err)
	}

	backupDB, err := sql.Open("sqlite", outputPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = backupDB.Close() }()

	var count int
	if err := backupDB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("COUNT(events) query error = %v", err)
	}
	if count != 1 {
		t.Fatalf("COUNT(events) = %d, want 1", count)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestDatasource_CreateBackup_doesNotOverwriteExistingFileWithoutForce(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut := newStoreManagementDatasource(t, dbPath, backupTestMigrations())
	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "backup", "traceary-backup.db")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	err := sut.CreateBackup(context.Background(), outputPath, false)
	if err == nil {
		t.Fatal("CreateBackup() error = nil, want error")
	}
}

func TestDatasource_CreateBackup_returnsErrorWhenSourceDoesNotExist(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "missing", "traceary.db")
	sut := newStoreManagementDatasource(t, dbPath, backupTestMigrations())
	outputPath := filepath.Join(t.TempDir(), "backup", "traceary-backup.db")

	err := sut.CreateBackup(context.Background(), outputPath, false)
	if err == nil {
		t.Fatal("CreateBackup() error = nil, want error")
	}
}

func TestDatasource_RestoreBackup(t *testing.T) {
	t.Parallel()

	sourceDBPath := filepath.Join(t.TempDir(), "source", "traceary.db")
	sourceDB := sqlite.NewDatabase(sourceDBPath, backupTestMigrations())
	sut := sqlite.NewStoreManagementDatasource(sourceDB)
	eventDS := sqlite.NewEventDatasource(sourceDB)
	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	event := newEventForSQLiteTest(
		t,
		"event-restore",
		"cli",
		"codex",
		"session-restore",
		"duck8823/traceary",
		"restored",
		time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	)
	if err := eventDS.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup", "traceary-backup.db")
	if err := sut.CreateBackup(context.Background(), backupPath, false); err != nil {
		t.Fatalf("CreateBackup() error = %v", err)
	}

	restoredDBPath := filepath.Join(t.TempDir(), "restored", "traceary.db")
	restoreSut := newStoreManagementDatasource(t, restoredDBPath, backupTestMigrations())
	if err := restoreSut.RestoreBackup(context.Background(), backupPath, false); err != nil {
		t.Fatalf("RestoreBackup() error = %v", err)
	}

	restoredDB, err := sql.Open("sqlite", restoredDBPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = restoredDB.Close() }()

	var count int
	if err := restoredDB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("COUNT(events) query error = %v", err)
	}
	if count != 1 {
		t.Fatalf("COUNT(events) = %d, want 1", count)
	}

	info, err := os.Stat(restoredDBPath)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("restored DB mode = %o, want 600", info.Mode().Perm())
	}
}

func TestDatasource_RestoreBackup_doesNotOverwriteExistingFileWithoutForce(t *testing.T) {
	t.Parallel()

	restoreDBPath := filepath.Join(t.TempDir(), "traceary.db")
	sut := newStoreManagementDatasource(t, restoreDBPath, backupTestMigrations())
	backupPath := filepath.Join(t.TempDir(), "backup", "traceary-backup.db")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("backup"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "restored", "traceary.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	err := sut.RestoreBackup(context.Background(), backupPath, false)
	if err == nil {
		t.Fatal("RestoreBackup() error = nil, want error")
	}
}

func TestDatasource_RestoreBackup_preservesExistingDBOnFailure(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "restored", "traceary.db")
	sut := newStoreManagementDatasource(t, dbPath, backupTestMigrations())
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	invalidBackupPath := filepath.Join(t.TempDir(), "backup", "invalid-backup.db")
	if err := os.MkdirAll(filepath.Dir(invalidBackupPath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(invalidBackupPath, []byte("not-a-sqlite-db"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	err := sut.RestoreBackup(context.Background(), invalidBackupPath, true)
	if err == nil {
		t.Fatal("RestoreBackup() error = nil, want error")
	}

	content, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(content) != "existing" {
		t.Fatalf("restored DB content = %q, want existing content to remain", string(content))
	}
}

func backupTestMigrations() fstest.MapFS {
	return fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    source_hook TEXT
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_events_created_at
    ON events(created_at DESC, id DESC);`),
		},
	}
}
