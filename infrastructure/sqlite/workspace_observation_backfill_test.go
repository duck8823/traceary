package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceObservationCatchUpBatchQuery_UsesPrimaryEventPartialIndex(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`
		CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL
		);
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			created_at TEXT NOT NULL,
			agent TEXT NOT NULL,
			source_hook TEXT
		);
		CREATE TABLE session_workspace_observations (
			observation_id TEXT PRIMARY KEY,
			observation_kind TEXT NOT NULL,
			observed_event_id TEXT
		);
		CREATE UNIQUE INDEX idx_session_workspace_observations_primary_event
			ON session_workspace_observations(observed_event_id)
			WHERE observation_kind = 'primary'
			  AND observed_event_id IS NOT NULL
			  AND observed_event_id <> '';
	`); err != nil {
		t.Fatalf("create query-plan schema: %v", err)
	}

	rows, err := db.QueryContext(
		context.Background(),
		"EXPLAIN QUERY PLAN "+workspaceObservationCatchUpBatchQuery,
		workspaceObservationCatchUpBatchSize,
	)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}

	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_session_workspace_observations_primary_event") {
		t.Errorf("query plan does not use primary-event partial index:\n%s", joined)
	}
	for _, detail := range plan {
		if strings.Contains(detail, "SCAN o") {
			t.Errorf("query plan scans session_workspace_observations in correlated lookup:\n%s", joined)
		}
	}
}

func TestCatchUpWorkspaceObservations_RetriesConcurrentWriterContention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "catch-up.db")
	lockDB, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = lockDB.Close() })
	lockDB.SetMaxOpenConns(1)

	if _, err := lockDB.Exec(`
		CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL
		);
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			created_at TEXT NOT NULL,
			agent TEXT NOT NULL,
			source_hook TEXT
		);
		CREATE TABLE session_workspace_observations (
			observation_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			raw_workspace TEXT,
			observation_kind TEXT NOT NULL,
			observation_origin TEXT NOT NULL,
			observed_relationship TEXT NOT NULL,
			observed_event_id TEXT,
			delivery_record_id TEXT,
			attribution_fingerprint TEXT NOT NULL,
			diagnostic_reason TEXT NOT NULL,
			observed_at TEXT NOT NULL,
			source_client TEXT NOT NULL,
			source_hook TEXT
		);
		CREATE UNIQUE INDEX idx_session_workspace_observations_primary_event
			ON session_workspace_observations(observed_event_id)
			WHERE observation_kind = 'primary'
			  AND observed_event_id IS NOT NULL
			  AND observed_event_id <> '';
		CREATE TABLE writer_lock (id INTEGER PRIMARY KEY);
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, agent, source_hook)
		VALUES ('event-1', 'session-1', '/repo', '2026-07-24T00:00:00Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("create catch-up schema: %v", err)
	}

	fastBusyDSN := strings.Replace(sqliteDSN(dbPath), "busy_timeout%281000%29", "busy_timeout%281%29", 1)
	catchUpDB, err := sql.Open("sqlite", fastBusyDSN)
	if err != nil {
		t.Fatalf("open catch-up DB: %v", err)
	}
	t.Cleanup(func() { _ = catchUpDB.Close() })
	catchUpDB.SetMaxOpenConns(1)

	locker, err := lockDB.Begin()
	if err != nil {
		t.Fatalf("begin writer lock: %v", err)
	}
	if _, err := locker.Exec(`INSERT INTO writer_lock (id) VALUES (1)`); err != nil {
		t.Fatalf("hold writer lock: %v", err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = locker.Commit()
		close(released)
	}()

	result, err := catchUpWorkspaceObservations(context.Background(), catchUpDB, 10)
	if err != nil {
		t.Fatalf("catchUpWorkspaceObservations() error = %v", err)
	}
	<-released
	if result.Selected != 1 || result.Inserted != 1 || result.Retries == 0 || result.MorePending {
		t.Fatalf("catch-up result = %+v, want selected=1 inserted=1 retries>0 more_pending=false", result)
	}

	var count int
	if err := catchUpDB.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations WHERE observed_event_id = 'event-1'`).Scan(&count); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != 1 {
		t.Fatalf("observation count = %d, want 1", count)
	}
}
