package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigration_CollapseSessionWorkspaceObservations_AggregatesByKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	legacy := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 76))
	if err := legacy.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-76) error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sessions (session_id, started_at, client, agent, workspace)
		VALUES ('session-1', '2026-07-22T00:00:00Z', 'hook', 'codex', '/repo');
		INSERT INTO workspace_observation_catchup_state(singleton, exhausted) VALUES (1, 1)
		ON CONFLICT(singleton) DO UPDATE SET exhausted = 1;
		INSERT INTO session_workspace_observations (
			observation_id, session_id, workspace, raw_workspace, observation_kind,
			observation_origin, observed_relationship, observed_event_id,
			delivery_record_id, attribution_fingerprint, diagnostic_reason,
			observed_at, source_client, source_hook
		) VALUES
			('o1', 'session-1', '/repo', NULL, 'primary', 'runtime', 'exact', 'event-a1', 'd-a1', 'fp-a1', '', '2026-07-22T00:00:00Z', 'codex', 'user_prompt_submit'),
			('o2', 'session-1', '/repo', NULL, 'primary', 'runtime', 'exact', 'event-a2', NULL, 'fp-a2', '', '2026-07-22T00:00:01Z', 'codex', 'user_prompt_submit'),
			('o3', 'session-1', '/repo', NULL, 'primary', 'runtime', 'exact', 'event-a3', NULL, 'fp-a3', '', '2026-07-22T00:00:02Z', 'codex', 'user_prompt_submit'),
			('o4', 'session-1', '/other', NULL, 'primary', 'runtime', 'conflict', 'event-b1', 'd-b1', 'fp-b1', '', '2026-07-22T00:00:03Z', 'codex', 'user_prompt_submit'),
			('o5', 'session-1', '/other', NULL, 'primary', 'backfill', 'conflict', 'event-b2', NULL, 'fp-b2', '', '2026-07-22T00:00:04Z', 'codex', 'user_prompt_submit'),
			('o6', 'session-1', '/other', NULL, 'primary', 'runtime', 'conflict', 'gone-event', 'd-b3', 'fp-b3', '', '2026-07-22T00:00:05Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("seed pre-collapse rows: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	current := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := current.InitializeAuthorized(ctx); err != nil {
		t.Fatalf("InitializeAuthorized(76) error = %v", err)
	}

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(after) error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2 keys", rows)
	}

	var exactCount int
	var exactEvent, firstAt, lastAt string
	if err := db.QueryRow(`
		SELECT observation_count, observed_event_id, first_observed_at, last_observed_at
		  FROM session_workspace_observations
		 WHERE workspace = '/repo'`).Scan(&exactCount, &exactEvent, &firstAt, &lastAt); err != nil {
		t.Fatalf("read /repo aggregate: %v", err)
	}
	if exactCount != 3 || exactEvent != "event-a3" || firstAt != "2026-07-22T00:00:00Z" || lastAt != "2026-07-22T00:00:02Z" {
		t.Fatalf("/repo aggregate count=%d event=%s first=%s last=%s", exactCount, exactEvent, firstAt, lastAt)
	}

	var conflictCount int
	var conflictEvent, origin string
	if err := db.QueryRow(`
		SELECT observation_count, observed_event_id, observation_origin
		  FROM session_workspace_observations
		 WHERE workspace = '/other'`).Scan(&conflictCount, &conflictEvent, &origin); err != nil {
		t.Fatalf("read /other aggregate: %v", err)
	}
	if conflictCount != 3 || conflictEvent != "gone-event" || origin != "runtime" {
		t.Fatalf("/other aggregate count=%d event=%s origin=%s", conflictCount, conflictEvent, origin)
	}

	var exhausted int
	var frontierNorm, frontierID string
	if err := db.QueryRow(`
		SELECT exhausted, frontier_created_at_norm, frontier_event_id
		  FROM workspace_observation_catchup_state WHERE singleton = 1`).Scan(&exhausted, &frontierNorm, &frontierID); err != nil {
		t.Fatalf("read catch-up state: %v", err)
	}
	if exhausted != 1 || frontierNorm != "" || frontierID != "" {
		t.Fatalf("catch-up state exhausted=%d frontier=(%s,%s), want exhausted preserved and frontier empty", exhausted, frontierNorm, frontierID)
	}
}

func TestMigration_CollapseSessionWorkspaceObservations_DropsPerEventUniqueIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	legacy := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 76))
	if err := legacy.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-76) error = %v", err)
	}
	current := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := current.InitializeAuthorized(ctx); err != nil {
		t.Fatalf("InitializeAuthorized(76) error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var primary, delivery, relationship int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_session_workspace_observations_primary_event'`).Scan(&primary); err != nil {
		t.Fatalf("count primary-event index: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_session_workspace_observations_delivery_attribution'`).Scan(&delivery); err != nil {
		t.Fatalf("count delivery-attribution index: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_session_workspace_observations_relationship'`).Scan(&relationship); err != nil {
		t.Fatalf("count relationship index: %v", err)
	}
	if primary != 0 || delivery != 0 || relationship != 1 {
		t.Fatalf("indexes primary=%d delivery=%d relationship=%d, want 0/0/1", primary, delivery, relationship)
	}
}
