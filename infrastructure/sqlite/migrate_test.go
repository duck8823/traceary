package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
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
	sut := newStoreManagementDatasource(t, dbPath, migrations)

	if err := sut.Initialize(context.Background()); err != nil {
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
	ds := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))

	if err := ds.Initialize(context.Background()); err != nil {
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

	wantMigrations := countOnDiskSQLiteMigrations(t)

	var migrationCount int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("count schema_migrations error = %v", err)
	}
	if migrationCount != wantMigrations {
		t.Errorf("schema_migrations count = %d, want %d", migrationCount, wantMigrations)
	}

	assertSessionSpawnMetadataSchema(t, db)
}

func TestMigrations_upgradeFromPreV014DatabaseAddsSessionSpawnMetadata(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preV014 := migrationsBeforeVersion(t, onDiskSQLiteMigrationDir(t), 14)
	ds := newStoreManagementDatasource(t, dbPath, preV014)
	if err := ds.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(pre-v0.14) error = %v", err)
	}

	seedPreV014SessionRow(t, dbPath)

	ds = newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := ds.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(upgrade) error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	assertSessionSpawnMetadataSchema(t, db)
	assertPreV014SessionMetadataDefaults(t, db)
}

func TestMigrations_appliesGapInVersionHistory(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	withoutV014 := migrationsInRangeExcluding(t, onDiskSQLiteMigrationDir(t), 1, 16, 14)
	ds := newStoreManagementDatasource(t, dbPath, withoutV014)
	if err := ds.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(without v0.14) error = %v", err)
	}

	ds = newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := ds.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(upgrade) error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	assertSessionSpawnMetadataSchema(t, db)
	assertMigrationApplied(t, db, 14)
}

func TestMigrations_idempotentOnExistingDatabase(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	ds := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))

	if err := ds.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() first error = %v", err)
	}
	if err := ds.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() second error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	wantMigrations := countOnDiskSQLiteMigrations(t)

	var count int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count error = %v", err)
	}
	if count != wantMigrations {
		t.Errorf("schema_migrations count = %d, want %d", count, wantMigrations)
	}
}

func countOnDiskSQLiteMigrations(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(onDiskSQLiteMigrationDir(t))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			count++
		}
	}
	return count
}

func seedPreV014SessionRow(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open(pre-v0.14 seed) error = %v", err)
	}
	defer func() { _ = db.Close() }()

	// This intentionally freezes the sessions table shape after migrations
	// 000001..000013 so the v14 upgrade path proves existing rows survive
	// the new nullable/defaulted metadata columns.
	_, err = db.Exec(`
INSERT INTO sessions (
    session_id,
    started_at,
    client,
    agent,
    workspace,
    label,
    summary,
    parent_session_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
		"pre-v014-session",
		"2026-04-11T12:00:00Z",
		"cli",
		"codex",
		"github.com/duck8823/traceary",
		"pre v0.14 row",
		"existing summary",
		nil,
	)
	if err != nil {
		t.Fatalf("seed pre-v0.14 session row error = %v", err)
	}
}

func assertPreV014SessionMetadataDefaults(t *testing.T, db *sql.DB) {
	t.Helper()

	var (
		spawnEventID sql.NullString
		subagentKind string
		spawnOrder   sql.NullInt64
	)
	if err := db.QueryRow(`
SELECT spawn_event_id, subagent_kind, spawn_order
  FROM sessions
 WHERE session_id = ?;`, "pre-v014-session").Scan(&spawnEventID, &subagentKind, &spawnOrder); err != nil {
		t.Fatalf("query upgraded pre-v0.14 session row error = %v", err)
	}
	if spawnEventID.Valid {
		t.Errorf("spawn_event_id = %q, want NULL", spawnEventID.String)
	}
	if subagentKind != "" {
		t.Errorf("subagent_kind = %q, want empty string", subagentKind)
	}
	if spawnOrder.Valid {
		t.Errorf("spawn_order = %d, want NULL", spawnOrder.Int64)
	}

	assertMigrationApplied(t, db, 14)
}

func assertMigrationApplied(t *testing.T, db *sql.DB, version int) {
	t.Helper()

	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?;`, version).Scan(&applied); err != nil {
		t.Fatalf("query migration %d application error = %v", version, err)
	}
	if applied != 1 {
		t.Errorf("schema_migrations version %d count = %d, want 1", version, applied)
	}
}

func migrationsInRangeExcluding(t *testing.T, dir string, minVersion, maxVersion, excludedVersion int) fstest.MapFS {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	migrations := fstest.MapFS{}
	foundExcludedVersion := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		version, err := sqliteMigrationVersion(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if version < minVersion || version > maxVersion {
			continue
		}
		if version == excludedVersion {
			foundExcludedVersion = true
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		migrations[entry.Name()] = &fstest.MapFile{Data: data}
	}
	if !foundExcludedVersion {
		t.Fatalf("migration version %d not found in %s", excludedVersion, dir)
	}
	return migrations
}

// migrationsBeforeVersion returns on-disk migrations whose numeric version is
// lower than maxVersion, preserving the history shape of an older database.
func migrationsBeforeVersion(t *testing.T, dir string, maxVersion int) fstest.MapFS {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	migrations := fstest.MapFS{}
	foundMaxVersion := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		version, err := sqliteMigrationVersion(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if version == maxVersion {
			foundMaxVersion = true
		}
		if version >= maxVersion {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		migrations[entry.Name()] = &fstest.MapFile{Data: data}
	}
	if !foundMaxVersion {
		t.Fatalf("migration version %d not found in %s", maxVersion, dir)
	}
	return migrations
}

func assertSessionSpawnMetadataSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(sessions) error = %v", err)
	}
	defer func() { _ = rows.Close() }()

	type column struct {
		typ        string
		notNull    bool
		defaultVal string
	}
	columns := map[string]column{}
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("scan table_info row error = %v", err)
		}
		columns[name] = column{
			typ:        strings.ToUpper(typ),
			notNull:    notNull == 1,
			defaultVal: defaultVal.String,
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info rows error = %v", err)
	}

	assertColumn := func(name, typ string, notNull bool, defaultVal string) {
		t.Helper()
		got, ok := columns[name]
		if !ok {
			t.Fatalf("sessions.%s column not found", name)
		}
		if got.typ != typ {
			t.Errorf("sessions.%s type = %q, want %q", name, got.typ, typ)
		}
		if got.notNull != notNull {
			t.Errorf("sessions.%s notNull = %v, want %v", name, got.notNull, notNull)
		}
		if got.defaultVal != defaultVal {
			t.Errorf("sessions.%s default = %q, want %q", name, got.defaultVal, defaultVal)
		}
	}
	assertColumn("spawn_event_id", "TEXT", false, "")
	assertColumn("subagent_kind", "TEXT", true, "''")
	assertColumn("spawn_order", "INTEGER", false, "")

	var indexCount int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_sessions_parent_spawn_order'`).Scan(&indexCount); err != nil {
		t.Fatalf("query index count error = %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("idx_sessions_parent_spawn_order index count = %d, want 1", indexCount)
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
	ds := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := ds.Initialize(context.Background()); err != nil {
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

	var sessionID, workspace, client, agent, startedAt string
	var endedAt *string
	if err := db2.QueryRow("SELECT session_id, workspace, client, agent, started_at, ended_at FROM sessions WHERE session_id = 's1'").Scan(&sessionID, &workspace, &client, &agent, &startedAt, &endedAt); err != nil {
		t.Fatalf("QueryRow sessions error = %v", err)
	}
	if workspace != "duck8823/traceary" {
		t.Errorf("workspace = %q, want duck8823/traceary", workspace)
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

func TestDatasource_Initialize_BackfillsWorkspaceObservationsInBoundedBatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrationDir := onDiskSQLiteMigrationDir(t)
	oldStore := newStoreManagementDatasource(t, dbPath, migrationsBeforeVersion(t, migrationDir, 22))
	if err := oldStore.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-22) error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (session_id, started_at, client, agent, workspace)
		VALUES ('session-backfill', '2026-07-22T00:00:00Z', 'hook', 'codex', '/repo')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	for i := 0; i < 1005; i++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, kind, client, agent, session_id, workspace, body, created_at, source_hook)
			VALUES (?, 'prompt', 'hook', 'codex', 'session-backfill', '/repo', 'historical', ?, 'user_prompt_submit')`,
			fmt.Sprintf("historical-%04d", i),
			fmt.Sprintf("2026-07-22T00:%02d:%02dZ", (i/60)%60, i%60),
		); err != nil {
			t.Fatalf("insert historical event %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(first catch-up) error = %v", err)
	}
	assertWorkspaceObservationMigrationCounts(t, dbPath, 1005, 1000)

	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(second catch-up) error = %v", err)
	}
	assertWorkspaceObservationMigrationCounts(t, dbPath, 1005, 1005)

	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(idempotent catch-up) error = %v", err)
	}
	assertWorkspaceObservationMigrationCounts(t, dbPath, 1005, 1005)
}

func TestDatasource_Initialize_CatchUpFailureDoesNotBlockRuntimeIngest(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations := onDiskSQLiteMigrations(t)
	store := newStoreManagementDatasource(t, dbPath, migrations)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(schema) error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sessions (session_id, started_at, client, agent, workspace)
		VALUES ('session-gap', '2026-07-22T00:00:00Z', 'hook', 'codex', '/repo');
		INSERT INTO events (id, kind, client, agent, session_id, workspace, body, created_at, source_hook)
		VALUES ('historical-gap', 'prompt', 'hook', 'codex', 'session-gap', '/repo', 'historical', '2026-07-22T00:00:00Z', 'user_prompt_submit');
		CREATE TRIGGER fail_workspace_backfill
		BEFORE INSERT ON session_workspace_observations
		WHEN NEW.observation_origin = 'backfill'
		BEGIN
			SELECT RAISE(ABORT, 'forced backfill failure');
		END;`); err != nil {
		t.Fatalf("seed catch-up failure: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close setup DB: %v", err)
	}

	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(catch-up failure) error = %v, want diagnostic-only success", err)
	}

	eventDS, _ := newEventDatasource(t, dbPath, migrations)
	runtimeEvent := model.EventOfWithSourceHook(
		types.EventID("runtime-after-gap"),
		types.EventKindPrompt,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID("session-gap"),
		types.Workspace("/repo"),
		"runtime ingest",
		time.Date(2026, 7, 22, 0, 1, 0, 0, time.UTC),
		"user_prompt_submit",
	)
	if err := eventDS.Save(ctx, runtimeEvent); err != nil {
		t.Fatalf("Save(runtime event) error = %v", err)
	}
	assertSQLiteCount(t, dbPath, "events", 2)
	assertSQLiteCount(t, dbPath, "session_workspace_observations", 1)
	assertSQLiteCountWhere(t, dbPath, "session_workspace_observations", "observation_origin = 'backfill'", 0)

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(recovery) error = %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_workspace_backfill`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close recovery DB: %v", err)
	}
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(retry catch-up) error = %v", err)
	}
	assertSQLiteCount(t, dbPath, "events", 2)
	assertSQLiteCount(t, dbPath, "session_workspace_observations", 2)
	assertSQLiteCountWhere(t, dbPath, "session_workspace_observations", "observation_origin = 'backfill'", 1)
}

func assertWorkspaceObservationMigrationCounts(t *testing.T, dbPath string, wantEvents, wantObservations int) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var events, observations, backfill, exact int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations`).Scan(&observations); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations WHERE observation_origin = 'backfill'`).Scan(&backfill); err != nil {
		t.Fatalf("count backfill observations: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations WHERE observed_relationship = 'exact'`).Scan(&exact); err != nil {
		t.Fatalf("count exact observations: %v", err)
	}
	if events != wantEvents || observations != wantObservations || backfill != wantObservations || exact != wantObservations {
		t.Fatalf("counts = events:%d observations:%d backfill:%d exact:%d, want %d/%d/%d/%d", events, observations, backfill, exact, wantEvents, wantObservations, wantObservations, wantObservations)
	}
}
