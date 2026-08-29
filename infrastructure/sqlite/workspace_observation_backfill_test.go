package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const aggregatedWorkspaceObservationSchema = `
		CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL
		);
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			created_at TEXT NOT NULL,
			created_at_norm TEXT NOT NULL,
			agent TEXT NOT NULL,
			source_hook TEXT
		);
		CREATE INDEX idx_events_created_at_norm_id_desc
			ON events(created_at_norm DESC, id DESC);
		CREATE TABLE session_workspace_observations (
			session_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			observed_relationship TEXT NOT NULL,
			source_client TEXT NOT NULL DEFAULT '',
			source_hook TEXT NOT NULL DEFAULT '',
			observation_kind TEXT NOT NULL,
			observation_count INTEGER NOT NULL DEFAULT 1,
			first_observed_at TEXT NOT NULL,
			last_observed_at TEXT NOT NULL,
			observed_event_id TEXT,
			raw_workspace TEXT,
			delivery_record_id TEXT,
			attribution_fingerprint TEXT NOT NULL,
			diagnostic_reason TEXT NOT NULL DEFAULT '',
			observation_origin TEXT NOT NULL,
			PRIMARY KEY (session_id, workspace, observed_relationship, source_client, source_hook, observation_kind)
		) WITHOUT ROWID;
`

const workspaceObservationCatchUpStateSchema = `
		CREATE TABLE workspace_observation_catchup_state (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			exhausted INTEGER NOT NULL,
			frontier_created_at_norm TEXT NOT NULL DEFAULT '',
			frontier_event_id TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO workspace_observation_catchup_state(singleton, exhausted) VALUES (1, 0);
`

func TestWorkspaceObservationCatchUpBatchQuery_UsesEventsCreatedAtNormIndex(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(aggregatedWorkspaceObservationSchema); err != nil {
		t.Fatalf("create query-plan schema: %v", err)
	}

	rows, err := db.QueryContext(
		context.Background(),
		"EXPLAIN QUERY PLAN "+workspaceObservationCatchUpBatchQuery,
		"2026-07-24T00:00:01.000000000Z",
		"event-z",
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
	if !strings.Contains(joined, "idx_events_created_at_norm_id_desc") {
		t.Errorf("query plan does not use idx_events_created_at_norm_id_desc:\n%s", joined)
	}
	if strings.Contains(joined, "ts_norm") {
		t.Errorf("query plan still wraps created_at in ts_norm:\n%s", joined)
	}
	for _, detail := range plan {
		if strings.Contains(detail, "SCAN") && strings.Contains(detail, "events") && !strings.Contains(detail, "INDEX") {
			t.Errorf("query plan table-scans events:\n%s", joined)
		}
	}
}

func TestCatchUpWorkspaceObservations_SkipsWhenExhausted(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(aggregatedWorkspaceObservationSchema + workspaceObservationCatchUpStateSchema + `
		UPDATE workspace_observation_catchup_state SET exhausted = 1 WHERE singleton = 1;
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent, source_hook)
		VALUES ('event-new', 'session-1', '/repo', '2026-07-24T00:00:02Z', '2026-07-24T00:00:02.000000000Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("create skip schema: %v", err)
	}

	result, err := catchUpWorkspaceObservations(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("catchUpWorkspaceObservations() error = %v", err)
	}
	if !result.Skipped || result.Selected != 0 || result.Inserted != 0 {
		t.Fatalf("catch-up result = %+v, want skipped with no selected rows", result)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations`).Scan(&count); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != 0 {
		t.Fatalf("observation count = %d, want 0 (exhausted skip does not backfill)", count)
	}
}

func TestCatchUpWorkspaceObservations_FillsWhenNewestEventIsMissing(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(aggregatedWorkspaceObservationSchema + `
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent, source_hook)
		VALUES ('event-new', 'session-1', '/repo', '2026-07-24T00:00:02Z', '2026-07-24T00:00:02.000000000Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("create fill schema: %v", err)
	}

	result, err := catchUpWorkspaceObservations(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("catchUpWorkspaceObservations() error = %v", err)
	}
	if result.Skipped || result.Selected != 1 || result.Inserted != 1 {
		t.Fatalf("catch-up result = %+v, want selected=1 inserted=1", result)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations WHERE observed_event_id = 'event-new'`).Scan(&count); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != 1 {
		t.Fatalf("observation count = %d, want 1", count)
	}
}

func TestCatchUpWorkspaceObservations_AdvancesHighWaterMark(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(aggregatedWorkspaceObservationSchema + workspaceObservationCatchUpStateSchema + `
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent, source_hook) VALUES
			('event-old', 'session-1', '/repo', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00.000000000Z', 'codex', 'user_prompt_submit'),
			('event-mid', 'session-1', '/repo', '2026-07-24T00:00:01Z', '2026-07-24T00:00:01.000000000Z', 'codex', 'user_prompt_submit'),
			('event-new', 'session-1', '/repo', '2026-07-24T00:00:02Z', '2026-07-24T00:00:02.000000000Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("create frontier schema: %v", err)
	}

	result, err := catchUpWorkspaceObservations(context.Background(), db, 2)
	if err != nil {
		t.Fatalf("catchUpWorkspaceObservations() error = %v", err)
	}
	if result.Selected != 2 || result.Inserted != 1 || !result.MorePending {
		t.Fatalf("catch-up result = %+v, want selected=2 inserted=1 more_pending", result)
	}

	var frontierNorm, frontierID string
	if err := db.QueryRow(`SELECT frontier_created_at_norm, frontier_event_id FROM workspace_observation_catchup_state WHERE singleton = 1`).Scan(&frontierNorm, &frontierID); err != nil {
		t.Fatalf("read frontier: %v", err)
	}
	if frontierID != "event-mid" {
		t.Fatalf("frontier event = %s, want event-mid (oldest of the newest two)", frontierID)
	}

	var newest string
	if err := db.QueryRow(`SELECT observed_event_id FROM session_workspace_observations`).Scan(&newest); err != nil {
		t.Fatalf("read newest observed event: %v", err)
	}
	if newest != "event-new" {
		t.Fatalf("observed_event_id = %s, want newest covered event-new", newest)
	}
}

func TestCatchUpWorkspaceObservations_ResumesAfterMark(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(aggregatedWorkspaceObservationSchema + workspaceObservationCatchUpStateSchema + `
		UPDATE workspace_observation_catchup_state
		   SET frontier_created_at_norm = '2026-07-24T00:00:01.000000000Z', frontier_event_id = 'event-mid'
		 WHERE singleton = 1;
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent, source_hook) VALUES
			('event-old', 'session-1', '/repo', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00.000000000Z', 'codex', 'user_prompt_submit'),
			('event-mid', 'session-1', '/repo', '2026-07-24T00:00:01Z', '2026-07-24T00:00:01.000000000Z', 'codex', 'user_prompt_submit'),
			('event-new', 'session-1', '/repo', '2026-07-24T00:00:02Z', '2026-07-24T00:00:02.000000000Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("create resume schema: %v", err)
	}

	result, err := catchUpWorkspaceObservations(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("catchUpWorkspaceObservations() error = %v", err)
	}
	if result.Selected != 1 || result.Inserted != 1 || result.MorePending {
		t.Fatalf("catch-up result = %+v, want only the event older than the frontier", result)
	}
	var observed string
	if err := db.QueryRow(`SELECT observed_event_id FROM session_workspace_observations`).Scan(&observed); err != nil {
		t.Fatalf("read observed event: %v", err)
	}
	if observed != "event-old" {
		t.Fatalf("observed_event_id = %s, want event-old", observed)
	}
}

func TestCatchUpWorkspaceObservations_MarksExhaustedOnShortBatch(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(aggregatedWorkspaceObservationSchema + workspaceObservationCatchUpStateSchema + `
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent, source_hook)
		VALUES ('event-1', 'session-1', '/repo', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00.000000000Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("create short-batch schema: %v", err)
	}

	first, err := catchUpWorkspaceObservations(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("first catch-up error = %v", err)
	}
	if first.Selected != 1 || first.Inserted != 1 {
		t.Fatalf("first catch-up = %+v, want selected=1 inserted=1", first)
	}
	second, err := catchUpWorkspaceObservations(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("second catch-up error = %v", err)
	}
	if second.Selected != 0 || second.Inserted != 0 {
		t.Fatalf("second catch-up = %+v, want selected=0 so exhausted can be marked", second)
	}
	var exhausted int
	if err := db.QueryRow(`SELECT exhausted FROM workspace_observation_catchup_state WHERE singleton = 1`).Scan(&exhausted); err != nil {
		t.Fatalf("read exhausted: %v", err)
	}
	if exhausted != 1 {
		t.Fatalf("exhausted = %d, want 1", exhausted)
	}
	third, err := catchUpWorkspaceObservations(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("third catch-up error = %v", err)
	}
	if !third.Skipped {
		t.Fatalf("third catch-up = %+v, want skipped", third)
	}
}

func TestCatchUpWorkspaceObservations_ReSweepDoesNotInflateCount(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(aggregatedWorkspaceObservationSchema + workspaceObservationCatchUpStateSchema + `
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent, source_hook)
		VALUES ('event-1', 'session-1', '/repo', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00.000000000Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("create resweep schema: %v", err)
	}

	if _, err := catchUpWorkspaceObservations(context.Background(), db, 10); err != nil {
		t.Fatalf("first catch-up error = %v", err)
	}
	if _, err := db.Exec(`UPDATE workspace_observation_catchup_state SET exhausted = 0, frontier_created_at_norm = '', frontier_event_id = '' WHERE singleton = 1`); err != nil {
		t.Fatalf("rewind frontier: %v", err)
	}
	result, err := catchUpWorkspaceObservations(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("resweep error = %v", err)
	}
	if result.Selected != 1 || result.Inserted != 0 {
		t.Fatalf("resweep = %+v, want selected=1 inserted=0", result)
	}
	var count, volume int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(observation_count), 0) FROM session_workspace_observations`).Scan(&count, &volume); err != nil {
		t.Fatalf("read volume: %v", err)
	}
	if count != 1 || volume != 1 {
		t.Fatalf("rows=%d volume=%d, want 1/1 after backfill resweep", count, volume)
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

	if _, err := lockDB.Exec(aggregatedWorkspaceObservationSchema + `
		CREATE TABLE writer_lock (id INTEGER PRIMARY KEY);
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent, source_hook)
		VALUES ('event-1', 'session-1', '/repo', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00.000000000Z', 'codex', 'user_prompt_submit');
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

func TestCatchUpWorkspaceObservations_DoesNotReportRolledBackInserts(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(aggregatedWorkspaceObservationSchema + `
		CREATE TRIGGER reject_second_observation
			BEFORE INSERT ON session_workspace_observations
			WHEN NEW.observed_event_id = 'event-2'
			BEGIN
				SELECT RAISE(ABORT, 'forced catch-up failure');
			END;
		INSERT INTO sessions (session_id, workspace) VALUES ('session-1', '/repo');
		INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent, source_hook)
		VALUES
			('event-1', 'session-1', '/repo', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00.000000000Z', 'codex', 'user_prompt_submit'),
			('event-2', 'session-1', '/other', '2026-07-24T00:00:01Z', '2026-07-24T00:00:01.000000000Z', 'codex', 'user_prompt_submit');
	`); err != nil {
		t.Fatalf("create catch-up schema: %v", err)
	}

	result, err := catchUpWorkspaceObservations(context.Background(), db, 10)
	if err == nil || !strings.Contains(err.Error(), "forced catch-up failure") {
		t.Fatalf("catchUpWorkspaceObservations() error = %v, want forced failure", err)
	}
	if result.Selected != 2 || result.Inserted != 0 || result.Retries != 0 {
		t.Fatalf("catch-up result = %+v, want selected=2 inserted=0 retries=0", result)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations`).Scan(&count); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != 0 {
		t.Fatalf("observation count = %d, want 0 after rollback", count)
	}
}
