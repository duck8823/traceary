package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_Initialize_ZeroPaddingなしでもバージョン順に適用する(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"2_create_events.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY
);`),
		},
		"10_insert_seed.sql": {
			Data: []byte(`
INSERT INTO events(id) VALUES ('seed');`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut := sqlite.NewDatasource(migrations)

	if err := sut.Initialize(context.Background(), dbPath); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events;`).Scan(&count); err != nil {
		t.Fatalf("events count query error = %v", err)
	}
	if count != 1 {
		t.Fatalf("events count = %d, want 1", count)
	}
}
