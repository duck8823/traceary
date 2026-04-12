package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_Initialize(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut := sqlite.NewDatasource(dbPath, migrations)

	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations;`).Scan(&count); err != nil {
		t.Fatalf("schema_migrations count query error = %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations count = %d, want 1", count)
	}

	if _, err := db.Exec(`SELECT 1 FROM events LIMIT 1;`); err != nil {
		t.Fatalf("events table query error = %v", err)
	}
}

func TestDatasource_Initialize_restrictsDirAndFilePermissions(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY
);`),
		},
	}
	dbDir := filepath.Join(t.TempDir(), "traceary-private")
	dbPath := filepath.Join(dbDir, "traceary.db")
	sut := sqlite.NewDatasource(dbPath, migrations)

	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	dirInfo, err := os.Stat(dbDir)
	if err != nil {
		t.Fatalf("os.Stat(dir) error = %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 700", dirInfo.Mode().Perm())
	}

	fileInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("os.Stat(file) error = %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", fileInfo.Mode().Perm())
	}
}
