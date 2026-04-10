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

func TestDatasource_Initialize_appliesMigrationsInVersionOrder(t *testing.T) {
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

func TestMigrations_applyToEmptyDatabase(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	ds := sqlite.NewDatasource(os.DirFS("../../schema/sqlite/migrations"))

	if err := ds.Initialize(context.Background(), dbPath); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	tables := []string{"events", "command_audits", "sessions", "schema_migrations"}
	for _, table := range tables {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			t.Fatalf("QueryRow(%s) error = %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %q not found", table)
		}
	}

	// Count migration files dynamically
	entries, err := os.ReadDir("../../schema/sqlite/migrations")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	wantMigrations := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".sql" {
			wantMigrations++
		}
	}

	var migrationCount int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("count schema_migrations error = %v", err)
	}
	if migrationCount != wantMigrations {
		t.Errorf("schema_migrations count = %d, want %d", migrationCount, wantMigrations)
	}
}

func TestMigrations_idempotentOnExistingDatabase(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	ds := sqlite.NewDatasource(os.DirFS("../../schema/sqlite/migrations"))

	if err := ds.Initialize(context.Background(), dbPath); err != nil {
		t.Fatalf("Initialize() first error = %v", err)
	}
	if err := ds.Initialize(context.Background(), dbPath); err != nil {
		t.Fatalf("Initialize() second error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	entries, err := os.ReadDir("../../schema/sqlite/migrations")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	wantMigrations := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".sql" {
			wantMigrations++
		}
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count error = %v", err)
	}
	if count != wantMigrations {
		t.Errorf("schema_migrations count = %d, want %d", count, wantMigrations)
	}
}

func TestMigrations_backfillPopulatesSessionsFromEvents(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	// Apply first 3 migrations manually to simulate pre-v0.1.18 database
	for _, m := range []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL);`,
		`CREATE TABLE events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, agent TEXT NOT NULL, session_id TEXT NOT NULL, body TEXT NOT NULL, created_at TEXT NOT NULL);`,
		`INSERT INTO schema_migrations(version, name, applied_at) VALUES (1, '000001_init.sql', '2026-01-01T00:00:00Z');`,
		`ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT ''; ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';`,
		`INSERT INTO schema_migrations(version, name, applied_at) VALUES (2, '000002_add_event_metadata.sql', '2026-01-01T00:00:00Z');`,
		`CREATE TABLE command_audits (event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE, command_text TEXT NOT NULL, input_text TEXT NOT NULL, output_text TEXT NOT NULL, input_truncated INTEGER NOT NULL DEFAULT 0, output_truncated INTEGER NOT NULL DEFAULT 0);`,
		`INSERT INTO schema_migrations(version, name, applied_at) VALUES (3, '000003_create_command_audits.sql', '2026-01-01T00:00:00Z');`,
	} {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("Exec error = %v: %s", err, m)
		}
	}

	// Insert events before migration 4
	if _, err := db.Exec(`INSERT INTO events (id, kind, agent, session_id, body, created_at, client, repo) VALUES ('e1', 'session_started', 'claude', 's1', 'session started', '2026-04-10T12:00:00Z', 'hook', 'duck8823/traceary')`); err != nil {
		t.Fatalf("Insert event error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events (id, kind, agent, session_id, body, created_at, client, repo) VALUES ('e2', 'session_ended', 'claude', 's1', 'session ended', '2026-04-10T13:00:00Z', 'hook', 'duck8823/traceary')`); err != nil {
		t.Fatalf("Insert event error = %v", err)
	}
	_ = db.Close()

	// Apply remaining migrations via Initialize
	ds := sqlite.NewDatasource(os.DirFS("../../schema/sqlite/migrations"))
	if err := ds.Initialize(context.Background(), dbPath); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db2.Close() }()

	var sessionCount int
	if err := db2.QueryRow("SELECT count(*) FROM sessions").Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions error = %v", err)
	}
	if sessionCount != 1 {
		t.Errorf("sessions count = %d, want 1", sessionCount)
	}

	var sessionID, repo, client, agent, startedAt string
	var endedAt *string
	if err := db2.QueryRow("SELECT session_id, repo, client, agent, started_at, ended_at FROM sessions WHERE session_id = 's1'").Scan(&sessionID, &repo, &client, &agent, &startedAt, &endedAt); err != nil {
		t.Fatalf("QueryRow sessions error = %v", err)
	}
	if repo != "duck8823/traceary" {
		t.Errorf("repo = %q, want duck8823/traceary", repo)
	}
	if client != "hook" {
		t.Errorf("client = %q, want hook", client)
	}
	if agent != "claude" {
		t.Errorf("agent = %q, want claude", agent)
	}
	if startedAt != "2026-04-10T12:00:00Z" {
		t.Errorf("started_at = %q, want 2026-04-10T12:00:00Z", startedAt)
	}
	if endedAt == nil {
		t.Fatal("ended_at should not be nil")
	}
	if *endedAt != "2026-04-10T13:00:00Z" {
		t.Errorf("ended_at = %q, want 2026-04-10T13:00:00Z", *endedAt)
	}
}
