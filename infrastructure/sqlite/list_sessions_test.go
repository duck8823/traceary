package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func listSessionsTestMigrations() fstest.MapFS {
	return fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0
);`),
		},
		"000004_create_sessions.sql": {
			Data: []byte(`CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    client TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    repo TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT REFERENCES sessions(session_id)
);
INSERT OR IGNORE INTO sessions (session_id, started_at, ended_at, client, agent, repo)
SELECT
    e.session_id,
    COALESCE(MIN(CASE WHEN e.kind = 'session_started' THEN e.created_at END), MIN(e.created_at)),
    MAX(CASE WHEN e.kind = 'session_ended' THEN e.created_at END),
    COALESCE(MAX(CASE WHEN e.kind = 'session_started' THEN e.client END), MAX(e.client)),
    COALESCE(MAX(CASE WHEN e.kind = 'session_started' THEN e.agent END), MAX(e.agent)),
    COALESCE(MAX(CASE WHEN e.kind = 'session_started' THEN e.repo END), MAX(e.repo))
FROM events e
GROUP BY e.session_id;`),
		},
	}
}

func saveTestSession(ctx context.Context, t *testing.T, ds *infra.Datasource, dbPath string, sessionID string, startedAt time.Time, endedAt *time.Time, agent string, repo string) {
	t.Helper()
	ag, _ := types.AgentOf(agent)
	sid, _ := types.SessionIDOf(sessionID)
	session := model.NewSession(sid, startedAt, "hook", ag, repo)
	if err := ds.SaveSession(ctx, dbPath, session); err != nil {
		t.Fatalf("SaveSession(start) error = %v", err)
	}
	if endedAt != nil {
		endSession := model.SessionOf(sid, startedAt, endedAt, "hook", ag, repo, "", "", "")
		if err := ds.SaveSession(ctx, dbPath, endSession); err != nil {
			t.Fatalf("SaveSession(end) error = %v", err)
		}
	}
}

func TestDatasource_ListSessionSummaries(t *testing.T) {
	t.Parallel()

	t.Run("retrieves session summaries", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		ds := infra.NewDatasource(listSessionsTestMigrations())
		ctx := context.Background()

		if err := ds.Initialize(ctx, dbPath); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		events := []struct {
			id        string
			kind      types.EventKind
			agent     string
			sessionID string
			body      string
		}{
			{"e1", types.EventKindSessionStarted, "claude", "s1", "session started"},
			{"e2", types.EventKindCommandExecuted, "claude", "s1", "go test ./..."},
			{"e3", types.EventKindCommandExecuted, "claude", "s1", "go build ./..."},
			{"e4", types.EventKindSessionEnded, "claude", "s1", "session ended"},
			{"e5", types.EventKindSessionStarted, "codex", "s2", "session started"},
			{"e6", types.EventKindCommandExecuted, "codex", "s2", "git status"},
		}

		for _, e := range events {
			eid, _ := types.EventIDOf(e.id)
			agent, _ := types.AgentOf(e.agent)
			sid, _ := types.SessionIDOf(e.sessionID)
			event, _ := model.NewEvent(eid, e.kind, "hook", agent, sid, "duck8823/traceary", e.body)
			if err := ds.Save(ctx, dbPath, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
		}

		// Save session metadata
		s1End := time.Now().UTC()
		saveTestSession(ctx, t, ds, dbPath, "s1", time.Now().Add(-time.Hour).UTC(), &s1End, "claude", "duck8823/traceary")
		saveTestSession(ctx, t, ds, dbPath, "s2", time.Now().UTC(), nil, "codex", "duck8823/traceary")

		summaries, err := ds.ListSessionSummaries(ctx, dbPath, queryservice.ListSessionsInput{
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("ListSessionSummaries() error = %v", err)
		}
		if len(summaries) != 2 {
			t.Fatalf("got %d summaries, want 2", len(summaries))
		}

		// s2 is newer (last inserted), should be first? Actually s1 has earlier started_at...
		// Both sessions have same created_at base (close in time), but order is by started_at DESC
		// s2's events are inserted after s1's, so s2.started_at > s1.started_at
		latest := summaries[0]
		if latest.SessionID != "s2" {
			t.Fatalf("first session = %q, want s2", latest.SessionID)
		}
		if latest.TotalEvents != 2 {
			t.Fatalf("s2 total_events = %d, want 2", latest.TotalEvents)
		}
		if latest.CommandCount != 1 {
			t.Fatalf("s2 command_count = %d, want 1", latest.CommandCount)
		}
		if latest.Status != "active" {
			t.Fatalf("s2 status = %q, want active", latest.Status)
		}

		older := summaries[1]
		if older.SessionID != "s1" {
			t.Fatalf("second session = %q, want s1", older.SessionID)
		}
		if older.TotalEvents != 4 {
			t.Fatalf("s1 total_events = %d, want 4", older.TotalEvents)
		}
		if older.CommandCount != 2 {
			t.Fatalf("s1 command_count = %d, want 2", older.CommandCount)
		}
		if older.Status != "ended" {
			t.Fatalf("s1 status = %q, want ended", older.Status)
		}
		if older.EndedAt == nil {
			t.Fatalf("s1 ended_at should not be nil")
		}
	})

	t.Run("agent filter works", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		ds := infra.NewDatasource(listSessionsTestMigrations())
		ctx := context.Background()

		if err := ds.Initialize(ctx, dbPath); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		for _, e := range []struct {
			id, agent, sid string
		}{
			{"e1", "claude", "s1"},
			{"e2", "codex", "s2"},
		} {
			eid, _ := types.EventIDOf(e.id)
			agent, _ := types.AgentOf(e.agent)
			sid, _ := types.SessionIDOf(e.sid)
			event, _ := model.NewEvent(eid, types.EventKindSessionStarted, "hook", agent, sid, "repo", "start")
			if err := ds.Save(ctx, dbPath, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
		}

		now := time.Now().UTC()
		saveTestSession(ctx, t, ds, dbPath, "s1", now, nil, "claude", "repo")
		saveTestSession(ctx, t, ds, dbPath, "s2", now.Add(time.Second), nil, "codex", "repo")

		summaries, err := ds.ListSessionSummaries(ctx, dbPath, queryservice.ListSessionsInput{
			Limit: 10,
			Agent: "claude",
		})
		if err != nil {
			t.Fatalf("ListSessionSummaries() error = %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if summaries[0].SessionID != "s1" {
			t.Fatalf("session = %q, want s1", summaries[0].SessionID)
		}
	})

	t.Run("from filter excludes out-of-range sessions", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		ds := infra.NewDatasource(listSessionsTestMigrations())
		ctx := context.Background()

		if err := ds.Initialize(ctx, dbPath); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		for _, e := range []struct {
			id, sid  string
			dayOfMon int
		}{
			{"e1", "s-old", 1},
			{"e2", "s-new", 10},
		} {
			eid, _ := types.EventIDOf(e.id)
			agent, _ := types.AgentOf("claude")
			sid, _ := types.SessionIDOf(e.sid)
			ts := time.Date(2026, 4, e.dayOfMon, 12, 0, 0, 0, time.UTC)
			event := model.EventOf(eid, types.EventKindSessionStarted, "hook", agent, sid, "repo", "start", ts)
			if err := ds.Save(ctx, dbPath, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			saveTestSession(ctx, t, ds, dbPath, e.sid, ts, nil, "claude", "repo")
		}

		fromDate := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
		summaries, err := ds.ListSessionSummaries(ctx, dbPath, queryservice.ListSessionsInput{
			Limit: 10,
			From:  &fromDate,
		})
		if err != nil {
			t.Fatalf("ListSessionSummaries() error = %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if summaries[0].SessionID != "s-new" {
			t.Fatalf("session = %q, want s-new", summaries[0].SessionID)
		}
	})

	t.Run("to filter excludes out-of-range sessions", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		ds := infra.NewDatasource(listSessionsTestMigrations())
		ctx := context.Background()

		if err := ds.Initialize(ctx, dbPath); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		for _, e := range []struct {
			id, sid  string
			dayOfMon int
		}{
			{"e1", "s-old", 1},
			{"e2", "s-new", 10},
		} {
			eid, _ := types.EventIDOf(e.id)
			agent, _ := types.AgentOf("claude")
			sid, _ := types.SessionIDOf(e.sid)
			ts := time.Date(2026, 4, e.dayOfMon, 12, 0, 0, 0, time.UTC)
			event := model.EventOf(eid, types.EventKindSessionStarted, "hook", agent, sid, "repo", "start", ts)
			if err := ds.Save(ctx, dbPath, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			saveTestSession(ctx, t, ds, dbPath, e.sid, ts, nil, "claude", "repo")
		}

		toDate := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
		summaries, err := ds.ListSessionSummaries(ctx, dbPath, queryservice.ListSessionsInput{
			Limit: 10,
			To:    &toDate,
		})
		if err != nil {
			t.Fatalf("ListSessionSummaries() error = %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if summaries[0].SessionID != "s-old" {
			t.Fatalf("session = %q, want s-old", summaries[0].SessionID)
		}
	})

}
