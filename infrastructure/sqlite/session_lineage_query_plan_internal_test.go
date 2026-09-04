package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestSessionLineageLatestEventUsesSessionCreatedAtNormIndexWithoutTempBTree(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/lineage.db"
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	schema := `
CREATE TABLE sessions (
    session_id TEXT PRIMARY KEY,
    workspace TEXT NOT NULL DEFAULT '',
    client TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    ended_at TEXT,
    label TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT REFERENCES sessions(session_id),
    spawn_event_id TEXT,
    subagent_kind TEXT NOT NULL DEFAULT '',
    spawn_order INTEGER,
    model TEXT NOT NULL DEFAULT ''
);
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    created_at_norm TEXT NOT NULL
);
CREATE INDEX idx_events_session_created_at_norm_id_desc
    ON events(session_id, created_at_norm DESC, id DESC);
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL
);
CREATE TABLE session_refinements (
    session_id TEXT PRIMARY KEY,
    summary TEXT NOT NULL DEFAULT ''
);
INSERT INTO sessions (session_id, started_at) VALUES ('root-session', '2026-08-16T00:00:00.000000000Z');
`
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+sessionLineageQuery, "root-session")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	assertLatestEventUsesCreatedAtNormIndex(t, plan)
}

func TestSessionTreeLatestEventUsesSessionCreatedAtNormIndexWithoutTempBTree(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/tree.db"
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	schema := `
CREATE TABLE sessions (
    session_id TEXT PRIMARY KEY,
    workspace TEXT NOT NULL DEFAULT '',
    client TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    ended_at TEXT,
    label TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT REFERENCES sessions(session_id),
    spawn_event_id TEXT,
    subagent_kind TEXT NOT NULL DEFAULT '',
    spawn_order INTEGER,
    model TEXT NOT NULL DEFAULT ''
);
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    created_at_norm TEXT NOT NULL
);
CREATE INDEX idx_events_session_created_at_norm_id_desc
    ON events(session_id, created_at_norm DESC, id DESC);
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL
);
CREATE TABLE session_refinements (
    session_id TEXT PRIMARY KEY,
    summary TEXT NOT NULL DEFAULT ''
);
INSERT INTO sessions (session_id, started_at) VALUES ('root-session', '2026-08-16T00:00:00.000000000Z');
`
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+listSessionTreeQuery, "root-session", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	assertLatestEventUsesCreatedAtNormIndex(t, plan)
}

func assertLatestEventUsesCreatedAtNormIndex(t *testing.T, plan []string) {
	t.Helper()
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_events_session_created_at_norm_id_desc") {
		t.Fatalf("plan does not use idx_events_session_created_at_norm_id_desc: %s", joined)
	}
	if strings.Contains(joined, "ts_norm(") {
		t.Fatalf("plan still ranks latest events through ts_norm(created_at): %s", joined)
	}
	for _, line := range plan {
		if strings.Contains(line, "USE TEMP B-TREE FOR ORDER BY") &&
			(strings.Contains(strings.ToLower(line), "event") || strings.Contains(line, "created_at")) {
			t.Fatalf("latest-event ranking sorts with a temp b-tree: %s\nfull plan:\n%s", line, joined)
		}
	}
}
