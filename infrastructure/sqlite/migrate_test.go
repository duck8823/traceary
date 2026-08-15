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

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
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

	tables := []string{"events", "event_metadata_projection", "command_audits", "sessions", "usage_observations", "run_lineages", "usage_observation_runs", "schema_migrations"}
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

func TestMigrations_usageObservationsAreAdditiveAndPreserveExistingRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preV027 := onDiskSQLiteMigrationsBefore(t, 27)
	store := newStoreManagementDatasource(t, dbPath, preV027)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-v027) error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(session_id, started_at, client, agent, workspace, label, summary, model, runtime_mode)
		VALUES ('existing-session', '2026-07-23T10:00:00Z', 'hook', 'codex', '/repo', 'keep-label', 'keep-summary', 'model-1', 'interactive')`); err != nil {
		t.Fatalf("insert existing session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events(id, kind, agent, session_id, body, created_at, client, workspace, body_availability)
		VALUES ('existing-event', 'note', 'codex', 'existing-session', 'keep-body', '2026-07-23T10:01:00Z', 'hook', '/repo', 'available')`); err != nil {
		t.Fatalf("insert existing event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store = newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.InitializeAuthorized(ctx); err != nil {
		t.Fatalf("Initialize(v027) error = %v", err)
	}
	db, err = sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var label, summary, body string
	if err := db.QueryRow(`SELECT label, summary FROM sessions WHERE session_id = 'existing-session'`).Scan(&label, &summary); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT body FROM events WHERE id = 'existing-event'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if label != "keep-label" || summary != "keep-summary" || body != "keep-body" {
		t.Fatalf("existing rows changed: label=%q summary=%q body=%q", label, summary, body)
	}
}

func TestMigrations_NormalizedTimestampColumnBackfillsAndMaintainsIndexes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 31)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-v031) error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events(id, kind, agent, session_id, body, created_at, client, workspace, body_availability)
		VALUES ('pre-v031-event', 'note', 'codex', 'session-1', 'existing body', '2026-07-25T00:00:00Z', 'hook', 'workspace-1', 'available')`); err != nil {
		_ = db.Close()
		t.Fatalf("seed pre-v031 event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).InitializeAuthorized(ctx); err != nil {
		t.Fatalf("Initialize(v031) error = %v", err)
	}
	db, err = sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var eventID string
	if err := db.QueryRow(`SELECT id FROM events WHERE id = 'pre-v031-event'`).Scan(&eventID); err != nil {
		t.Fatalf("read upgraded event identity: %v", err)
	}
	if eventID != "pre-v031-event" {
		t.Fatalf("upgraded event id = %q, want preserved identity", eventID)
	}
	var normalized string
	if err := db.QueryRow(`SELECT created_at_norm FROM events WHERE id = 'pre-v031-event'`).Scan(&normalized); err != nil {
		t.Fatalf("read upgraded normalized timestamp: %v", err)
	}
	if normalized != "2026-07-25T00:00:00.000000000Z" {
		t.Fatalf("upgraded normalized timestamp = %q", normalized)
	}
	if _, err := db.Exec(`UPDATE events SET created_at = '2026-07-25T00:00:00.5Z' WHERE id = 'pre-v031-event'`); err != nil {
		t.Fatalf("update migrated event timestamp: %v", err)
	}
	if err := db.QueryRow(`SELECT created_at_norm FROM events WHERE id = 'pre-v031-event'`).Scan(&normalized); err != nil {
		t.Fatalf("read maintained normalized timestamp: %v", err)
	}
	if normalized != "2026-07-25T00:00:00.500000000Z" {
		t.Fatalf("maintained normalized timestamp = %q", normalized)
	}
	if _, err := db.Exec(`INSERT INTO events(id, kind, agent, session_id, body, created_at, client, workspace, body_availability)
		VALUES ('post-v031-event', 'note', 'codex', 'session-1', 'new body', '2026-07-25T00:00:01Z', 'hook', 'workspace-1', 'available')`); err != nil {
		t.Fatalf("insert post-v031 event: %v", err)
	}
	if err := db.QueryRow(`SELECT created_at_norm FROM events WHERE id = 'post-v031-event'`).Scan(&normalized); err != nil {
		t.Fatalf("read inserted normalized timestamp: %v", err)
	}
	if normalized != "2026-07-25T00:00:01.000000000Z" {
		t.Fatalf("inserted normalized timestamp = %q", normalized)
	}
	for _, index := range []string{
		"idx_events_created_at_norm_id_desc",
		"idx_events_workspace_created_at_norm_id_desc",
		"idx_events_session_created_at_norm_id_desc",
		"idx_events_workspace_session_created_at_norm_id_desc",
		"idx_events_source_hook_created_at_norm_id_desc",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil {
			t.Fatalf("look up index %q: %v", index, err)
		}
		if count != 1 {
			t.Errorf("index %q count = %d, want 1", index, count)
		}
	}
}

func TestMigrations_NormalizedTimestampMatchesTSNormForLegacyEdgeValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 31)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-v031) error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		id        string
		createdAt string
	}{
		{id: "exact-second", createdAt: "2026-07-25T00:00:00Z"},
		{id: "fractional-second", createdAt: "2026-07-25T00:00:00.5Z"},
		{id: "nanosecond", createdAt: "2026-07-25T00:00:00.123456789Z"},
		{id: "beyond-nanosecond", createdAt: "2026-07-25T00:00:00.123456789123Z"},
		{id: "legacy-without-zone", createdAt: "2026-07-25T00:00:02.25"},
	}
	for _, fixture := range fixtures {
		if _, err := db.Exec(`INSERT INTO events(id, kind, agent, session_id, body, created_at, client, workspace, body_availability)
			VALUES (?, 'note', 'codex', 'session-1', 'legacy body', ?, 'hook', 'workspace-1', 'available')`, fixture.id, fixture.createdAt); err != nil {
			_ = db.Close()
			t.Fatalf("seed %s: %v", fixture.id, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).InitializeAuthorized(ctx); err != nil {
		t.Fatalf("Initialize(v031) error = %v", err)
	}
	db, err = sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, fixture := range fixtures {
		t.Run(fixture.id, func(t *testing.T) {
			var normalized, legacyNormalized string
			if err := db.QueryRow(
				`SELECT created_at_norm, ts_norm(created_at) FROM events WHERE id = ?`,
				fixture.id,
			).Scan(&normalized, &legacyNormalized); err != nil {
				t.Fatalf("read normalized timestamp: %v", err)
			}
			if normalized != legacyNormalized {
				t.Fatalf("created_at_norm = %q, ts_norm(created_at) = %q", normalized, legacyNormalized)
			}
		})
	}
}

func TestMigrations_RunLineageIsAdditiveAndPreservesV27Usage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preV028 := onDiskSQLiteMigrationsBefore(t, 28)
	if err := newStoreManagementDatasource(t, dbPath, preV028).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-v028): %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO usage_observations (
observation_id, session_id, host, source_name, source_version, scope, accounting, status, observed_at,
input_state, cached_input_state, cache_write_input_state, output_state, reasoning_output_state, total_state, cost_state
) VALUES ('legacy-v27', 'external-session', 'codex', 'legacy', '27', 'call', 'additive', 'pending', '2026-07-23T10:00:00Z',
'unknown', 'unknown', 'unknown', 'unknown', 'unknown', 'unknown', 'unknown')`)
	if err != nil {
		t.Fatalf("insert v27 usage: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(v28): %v", err)
	}
	id, _ := types.UsageObservationIDFrom("legacy-v27")
	restored, err := sqlite.NewUsageObservationDatasource(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))).FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	observation, present := restored.Value()
	if !present {
		t.Fatal("legacy usage missing")
	}
	if _, present := observation.Descriptor().RunIdentity().Value(); present {
		t.Fatal("legacy usage fabricated run identity")
	}
}

func TestMigrations_hookDeliveryAttemptsBackfillBodyFreeDenominator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preV023 := migrationsBeforeVersion(t, onDiskSQLiteMigrationDir(t), 23)
	store := newStoreManagementDatasource(t, dbPath, preV023)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-v023) error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO hook_deliveries (
		delivery_record_id, session_id, reported_delivery_id, delivery_fingerprint,
		identity_status, observed_event_id, accepted_at, source_client, source_hook
	) VALUES ('delivery-1', 'session-1', 'native-1', 'fingerprint-1', 'accepted',
		'event-1', '2026-07-22T00:00:00Z', 'codex', 'user_prompt_submit')`); err != nil {
		t.Fatalf("insert pre-v023 delivery: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-v023 DB: %v", err)
	}

	store = newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(v023) error = %v", err)
	}
	db, err = sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open(upgraded) error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var eventID, outcome, origin, observedAt string
	if err := db.QueryRow(`SELECT attempted_event_id, outcome, attempt_origin, observed_at FROM hook_delivery_attempts WHERE delivery_record_id = 'delivery-1'`).Scan(&eventID, &outcome, &origin, &observedAt); err != nil {
		t.Fatalf("read backfilled attempt: %v", err)
	}
	if eventID != "event-1" || outcome != "accepted" || origin != "backfill" || observedAt != "2026-07-22T00:00:00Z" {
		t.Fatalf("backfilled attempt = %q/%q/%q/%q", eventID, outcome, origin, observedAt)
	}
}

func TestMigrations_searchProjectionLifecycleBackfillsExactTerminalState(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"rebuilding", "complete", "failed", "drifted"} {
		state := state
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			store := newStoreManagementDatasource(t, dbPath, migrationsBeforeVersion(t, onDiskSQLiteMigrationDir(t), 41))
			if err := store.Initialize(ctx); err != nil {
				t.Fatalf("Initialize(pre-v041) error = %v", err)
			}
			db, err := sql.Open("sqlite", "file:"+dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE search_projection_state SET generation_id='legacy-generation',state=? WHERE singleton=1`, state); err != nil {
				t.Fatalf("seed legacy state: %v", err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			store = newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
			if err = store.Initialize(ctx); err != nil {
				t.Fatalf("Initialize(v041) error = %v", err)
			}
			db, err = sql.Open("sqlite", "file:"+dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			var got string
			if err = db.QueryRow(`SELECT state FROM search_projection_generation_lifecycle WHERE generation_id='legacy-generation'`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			// Migration 041 backfills the terminal state. Catch-up then applies
			// capacity-semantics obsolescence (#1679 / #1752): an in-flight
			// rebuild or drift at the DEFAULT version is abandoned so a
			// replacement can start. Complete and failed keep their row.
			want := state
			if state == "rebuilding" || state == "drifted" {
				want = "abandoned"
			}
			if got != want {
				t.Fatalf("lifecycle state=%q, want %q", got, want)
			}
		})
	}
}

func TestMigrations_searchProjectionOriginBackfillsOperatorVsAutomatic(t *testing.T) {
	t.Parallel()

	// The 064 UPDATE hardcodes the then-current default hash. Keep it equal
	// to DefaultSearchProjectionBudget so a later default change updates both
	// the pin test and this historical backfill string together.
	const defaultHashAt064 = "v4:8388608:8388608:8388608:2592000000000000:1535115264"
	if got := apptypes.DefaultSearchProjectionBudget().ConfigHash(); got != defaultHashAt064 {
		t.Fatalf("064 backfill hash %q != DefaultSearchProjectionBudget %q", defaultHashAt064, got)
	}

	for _, tc := range []struct {
		name string
		hash string
		want string
	}{
		{name: "064-era default stays automatic", hash: defaultHashAt064, want: "automatic"},
		{name: "custom hash becomes operator", hash: "v4:1:1:1:1:1", want: "operator"},
		{name: "empty hash stays automatic", hash: "", want: "automatic"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			store := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 64))
			if err := store.Initialize(ctx); err != nil {
				t.Fatalf("Initialize(pre-v064) error = %v", err)
			}
			db, err := sql.Open("sqlite", "file:"+dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE search_projection_state SET config_hash=? WHERE singleton=1`, tc.hash); err != nil {
				t.Fatalf("seed hash: %v", err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			store = newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
			if err = store.Initialize(ctx); err != nil {
				t.Fatalf("Initialize(v064) error = %v", err)
			}
			db, err = sql.Open("sqlite", "file:"+dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			var got string
			if err = db.QueryRow(`SELECT origin FROM search_projection_state WHERE singleton=1`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("origin=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestMigrations_sessionLifecycleStatePreservesLegacyRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preV024 := migrationsBeforeVersion(t, onDiskSQLiteMigrationDir(t), 24)
	store := newStoreManagementDatasource(t, dbPath, preV024)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-v024) error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open(pre-v024) error = %v", err)
	}
	_, err = db.Exec(`INSERT INTO sessions (
		session_id, started_at, ended_at, client, agent, workspace, label, summary, model
	) VALUES
		('legacy-active', '2026-07-22T10:00:00Z', NULL, 'hook', 'codex', 'workspace-a', 'active label', 'active summary', 'model-a'),
		('legacy-ended', '2026-07-22T11:00:00Z', '2026-07-22T11:30:00Z', 'hook', 'codex', 'workspace-b', 'ended label', 'ended summary', 'model-b')`)
	if err != nil {
		t.Fatalf("insert legacy sessions: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-v024 DB: %v", err)
	}

	store = newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(v024) error = %v", err)
	}
	db, err = sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open(upgraded) error = %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, tt := range []struct {
		sessionID     string
		wantStarted   string
		wantEnded     string
		wantReason    string
		wantWorkspace string
		wantLabel     string
		wantSummary   string
		wantModel     string
	}{
		{sessionID: "legacy-active", wantStarted: "2026-07-22T10:00:00Z", wantReason: "", wantWorkspace: "workspace-a", wantLabel: "active label", wantSummary: "active summary", wantModel: "model-a"},
		{sessionID: "legacy-ended", wantStarted: "2026-07-22T11:00:00Z", wantEnded: "2026-07-22T11:30:00Z", wantReason: "legacy_unknown", wantWorkspace: "workspace-b", wantLabel: "ended label", wantSummary: "ended summary", wantModel: "model-b"},
	} {
		var mode, reason, started, client, agent, workspace, label, summary, modelName string
		var ended sql.NullString
		if err := db.QueryRow(`SELECT runtime_mode, terminal_reason, started_at, ended_at, client, agent, workspace, label, summary, model FROM sessions WHERE session_id = ?`, tt.sessionID).Scan(&mode, &reason, &started, &ended, &client, &agent, &workspace, &label, &summary, &modelName); err != nil {
			t.Fatalf("read upgraded %s: %v", tt.sessionID, err)
		}
		if mode != "interactive" || reason != tt.wantReason || started != tt.wantStarted || ended.String != tt.wantEnded || client != "hook" || agent != "codex" || workspace != tt.wantWorkspace || label != tt.wantLabel || summary != tt.wantSummary || modelName != tt.wantModel {
			t.Fatalf("upgraded %s = mode=%q reason=%q started=%q ended=%q client=%q agent=%q workspace=%q label=%q summary=%q model=%q", tt.sessionID, mode, reason, started, ended.String, client, agent, workspace, label, summary, modelName)
		}
	}
	if _, err := db.Exec(`INSERT INTO sessions(session_id, started_at, runtime_mode) VALUES ('invalid-runtime', '2026-07-22T12:00:00Z', 'unknown')`); err == nil {
		t.Fatal("invalid runtime_mode insert succeeded")
	}
	if _, err := db.Exec(`INSERT INTO sessions(session_id, started_at, terminal_reason) VALUES ('invalid-reason', '2026-07-22T12:00:00Z', 'unknown')`); err == nil {
		t.Fatal("invalid terminal_reason insert succeeded")
	}
	if _, err := db.Exec(`INSERT INTO sessions(session_id, started_at, terminal_reason) VALUES ('invalid-active-reason', '2026-07-22T12:00:00Z', 'success')`); err == nil {
		t.Fatal("active session with terminal reason insert succeeded")
	}
	if _, err := db.Exec(`UPDATE sessions SET ended_at = NULL, terminal_reason = 'failure' WHERE session_id = 'legacy-ended'`); err == nil {
		t.Fatal("active session with terminal reason update succeeded")
	}
	if _, err := db.Exec(`UPDATE sessions SET ended_at = '2026-07-22T12:30:00Z', terminal_reason = '' WHERE session_id = 'legacy-active'`); err != nil {
		t.Fatalf("legacy-compatible end without terminal reason failed: %v", err)
	}
	assertMigrationApplied(t, db, 24)
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

func TestMigrations_rejectsAppliedVersionRecordedUnderDifferentName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	// Simulate an unreleased development store whose migration 44 was the
	// abandoned archive-sequence-inventory migration, later replaced in the
	// catalog by a different migration with the same version number.
	abandoned := migrationsBeforeVersion(t, onDiskSQLiteMigrationDir(t), 44)
	abandoned["000044_add_archive_sequence_inventory.sql"] = &fstest.MapFile{
		Data: []byte(`CREATE TABLE archive_sequence_inventory (sequence INTEGER PRIMARY KEY);`),
	}
	if err := newStoreManagementDatasource(t, dbPath, abandoned).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(abandoned catalog) error = %v", err)
	}

	err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).Initialize(ctx)
	if err == nil {
		t.Fatal("Initialize(replacement catalog) accepted a ledger recorded under the abandoned migration 44 name")
	}
	if !strings.Contains(err.Error(), "000044_add_archive_sequence_inventory.sql") {
		t.Fatalf("Initialize(replacement catalog) error = %v, want the abandoned migration name identified", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='archive_segments'`).Scan(&count); err != nil {
		t.Fatalf("look up archive_segments error = %v", err)
	}
	if count != 0 {
		t.Fatalf("replacement migration 44 ran against a rejected ledger: archive_segments count = %d", count)
	}
}

func TestMigrations_rejectsAbandonedDevelopmentStoreWithVersionsAboveCatalogMaximum(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	// Simulate an unreleased development store that applied the abandoned
	// migrations 44 and 45. Applied versions above the current catalog maximum
	// stay tolerated on their own so a store upgraded by a newer release
	// remains openable after a binary rollback (see
	// TestMigrations_EventMetadataProjectionSupportsPreProjectionWriterRollback),
	// but migrations apply in version order, so every abandoned store also
	// recorded the replaced version 44 under its old name and must fail closed.
	abandoned := migrationsBeforeVersion(t, onDiskSQLiteMigrationDir(t), 44)
	abandoned["000044_add_archive_sequence_inventory.sql"] = &fstest.MapFile{
		Data: []byte(`CREATE TABLE archive_sequence_inventory (sequence INTEGER PRIMARY KEY);`),
	}
	abandoned["000045_add_segment_catalog_ledger.sql"] = &fstest.MapFile{
		Data: []byte(`CREATE TABLE segment_catalog_ledger (id INTEGER PRIMARY KEY);`),
	}
	if err := newStoreManagementDatasource(t, dbPath, abandoned).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(abandoned catalog) error = %v", err)
	}

	err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).Initialize(ctx)
	if err == nil {
		t.Fatal("Initialize(current catalog) accepted an abandoned development store")
	}
	if !strings.Contains(err.Error(), "000044_add_archive_sequence_inventory.sql") {
		t.Fatalf("Initialize(current catalog) error = %v, want the abandoned migration 44 identified", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='archive_segments'`).Scan(&count); err != nil {
		t.Fatalf("look up archive_segments error = %v", err)
	}
	if count != 0 {
		t.Fatalf("replacement migration 44 ran against a rejected ledger: archive_segments count = %d", count)
	}
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
	if err := ds.InitializeAuthorized(context.Background()); err != nil {
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
	if err := store.InitializeAuthorized(ctx); err != nil {
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
