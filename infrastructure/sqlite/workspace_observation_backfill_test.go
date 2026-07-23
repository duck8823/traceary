package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
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
