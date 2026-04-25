package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

// listSessionsFixture bundles the per-aggregate datasources required by
// list_sessions tests.
type listSessionsFixture struct {
	eventDS      *infra.EventDatasource
	sessionDS    *infra.SessionDatasource
	storeManager *infra.StoreManagementDatasource
}

func newListSessionsFixture(t *testing.T, dbPath string, migrations fstest.MapFS) *listSessionsFixture {
	t.Helper()
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, migrations)
	return &listSessionsFixture{eventDS: eventDS, sessionDS: sessionDS, storeManager: storeManager}
}

func listSessionsTestMigrations() fstest.MapFS {
	return fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    source_hook TEXT
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER
);`),
		},
		"000004_create_sessions.sql": {
			Data: []byte(`CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    client TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    workspace TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT REFERENCES sessions(session_id)
);
INSERT OR IGNORE INTO sessions (session_id, started_at, ended_at, client, agent, workspace)
SELECT
    e.session_id,
    COALESCE(MIN(CASE WHEN e.kind = 'session_started' THEN e.created_at END), MIN(e.created_at)),
    MAX(CASE WHEN e.kind = 'session_ended' THEN e.created_at END),
    COALESCE(MAX(CASE WHEN e.kind = 'session_started' THEN e.client END), MAX(e.client)),
    COALESCE(MAX(CASE WHEN e.kind = 'session_started' THEN e.agent END), MAX(e.agent)),
    COALESCE(MAX(CASE WHEN e.kind = 'session_started' THEN e.workspace END), MAX(e.workspace))
FROM events e
GROUP BY e.session_id;`),
		},
	}
}

func saveTestSession(ctx context.Context, t *testing.T, ds *infra.SessionDatasource, sessionID string, startedAt time.Time, endedAt types.Optional[time.Time], agent string, workspace string) {
	t.Helper()
	ag, _ := types.AgentFrom(agent)
	sid, _ := types.SessionIDFrom(sessionID)
	session := model.SessionOf(sid, startedAt, endedAt, types.Client("hook"), ag, types.Workspace(workspace), "", "", types.SessionID(""))
	if err := ds.SaveSessionBoundaryForTest(ctx, session); err != nil {
		t.Fatalf("SaveSessionBoundaryForTest() error = %v", err)
	}
}

func TestDatasource_ListSummaries(t *testing.T) {
	t.Parallel()

	t.Run("retrieves session summaries", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
		ctx := context.Background()

		if err := fixture.storeManager.Initialize(ctx); err != nil {
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
			eid, _ := types.EventIDFrom(e.id)
			agent, _ := types.AgentFrom(e.agent)
			sid, _ := types.SessionIDFrom(e.sessionID)
			event, _ := model.NewEvent(eid, e.kind, "hook", agent, sid, "duck8823/traceary", e.body)
			if err := fixture.eventDS.Save(ctx, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
		}

		// Save session metadata
		s1End := time.Now().UTC()
		saveTestSession(ctx, t, fixture.sessionDS, "s1", time.Now().Add(-time.Hour).UTC(), types.Some(s1End), "claude", "duck8823/traceary")
		saveTestSession(ctx, t, fixture.sessionDS, "s2", time.Now().UTC(), types.None[time.Time](), "codex", "duck8823/traceary")

		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID(""), types.Workspace(""), types.Client(""), types.Agent(""), "", types.None[time.Time](), types.None[time.Time]())
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(summaries) != 2 {
			t.Fatalf("got %d summaries, want 2", len(summaries))
		}

		// s2 is newer (last inserted), should be first? Actually s1 has earlier started_at...
		// Both sessions have same created_at base (close in time), but order is by started_at DESC
		// s2's events are inserted after s1's, so s2.started_at > s1.started_at
		latest := summaries[0]
		if diff := cmp.Diff("s2", latest.SessionID().String()); diff != "" {
			t.Fatalf("first session mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(2, latest.TotalEvents()); diff != "" {
			t.Fatalf("s2 total_events mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(1, latest.CommandCount()); diff != "" {
			t.Fatalf("s2 command_count mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("active", latest.Status()); diff != "" {
			t.Fatalf("s2 status mismatch (-want +got):\n%s", diff)
		}

		older := summaries[1]
		if diff := cmp.Diff("s1", older.SessionID().String()); diff != "" {
			t.Fatalf("second session mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(4, older.TotalEvents()); diff != "" {
			t.Fatalf("s1 total_events mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(2, older.CommandCount()); diff != "" {
			t.Fatalf("s1 command_count mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("ended", older.Status()); diff != "" {
			t.Fatalf("s1 status mismatch (-want +got):\n%s", diff)
		}
		if _, ok := older.EndedAt().Value(); !ok {
			t.Fatalf("s1 ended_at should not be empty")
		}
	})

	t.Run("agent filter works", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
		ctx := context.Background()

		if err := fixture.storeManager.Initialize(ctx); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		for _, e := range []struct {
			id, agent, sid string
		}{
			{"e1", "claude", "s1"},
			{"e2", "codex", "s2"},
		} {
			eid, _ := types.EventIDFrom(e.id)
			agent, _ := types.AgentFrom(e.agent)
			sid, _ := types.SessionIDFrom(e.sid)
			event, _ := model.NewEvent(eid, types.EventKindSessionStarted, "hook", agent, sid, "workspace", "start")
			if err := fixture.eventDS.Save(ctx, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
		}

		now := time.Now().UTC()
		saveTestSession(ctx, t, fixture.sessionDS, "s1", now, types.None[time.Time](), "claude", "workspace")
		saveTestSession(ctx, t, fixture.sessionDS, "s2", now.Add(time.Second), types.None[time.Time](), "codex", "workspace")

		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID(""), types.Workspace(""), types.Client(""), types.Agent("claude"), "", types.None[time.Time](), types.None[time.Time]())
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if diff := cmp.Diff("s1", summaries[0].SessionID().String()); diff != "" {
			t.Fatalf("session mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("from filter excludes out-of-range sessions", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
		ctx := context.Background()

		if err := fixture.storeManager.Initialize(ctx); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		for _, e := range []struct {
			id, sid  string
			dayOfMon int
		}{
			{"e1", "s-old", 1},
			{"e2", "s-new", 10},
		} {
			eid, _ := types.EventIDFrom(e.id)
			agent, _ := types.AgentFrom("claude")
			sid, _ := types.SessionIDFrom(e.sid)
			ts := time.Date(2026, 4, e.dayOfMon, 12, 0, 0, 0, time.UTC)
			event := model.EventOf(eid, types.EventKindSessionStarted, "hook", agent, sid, "workspace", "start", ts)
			if err := fixture.eventDS.Save(ctx, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			saveTestSession(ctx, t, fixture.sessionDS, e.sid, ts, types.None[time.Time](), "claude", "workspace")
		}

		fromDate := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID(""), types.Workspace(""), types.Client(""), types.Agent(""), "", types.Some(fromDate), types.None[time.Time]())
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if diff := cmp.Diff("s-new", summaries[0].SessionID().String()); diff != "" {
			t.Fatalf("session mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("session_id filter returns only the matching session", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
		ctx := context.Background()

		if err := fixture.storeManager.Initialize(ctx); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		for _, e := range []struct {
			id, agent, sid string
		}{
			{"e1", "claude", "s1"},
			{"e2", "claude", "s2"},
		} {
			eid, _ := types.EventIDFrom(e.id)
			agent, _ := types.AgentFrom(e.agent)
			sid, _ := types.SessionIDFrom(e.sid)
			event, _ := model.NewEvent(eid, types.EventKindSessionStarted, "hook", agent, sid, "workspace", "start")
			if err := fixture.eventDS.Save(ctx, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
		}

		now := time.Now().UTC()
		saveTestSession(ctx, t, fixture.sessionDS, "s1", now, types.None[time.Time](), "claude", "workspace")
		saveTestSession(ctx, t, fixture.sessionDS, "s2", now.Add(time.Second), types.None[time.Time](), "claude", "workspace")

		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID("s1"), types.Workspace(""), types.Client(""), types.Agent(""), "", types.None[time.Time](), types.None[time.Time]())
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if diff := cmp.Diff("s1", summaries[0].SessionID().String()); diff != "" {
			t.Fatalf("session mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("to filter excludes out-of-range sessions", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
		ctx := context.Background()

		if err := fixture.storeManager.Initialize(ctx); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		for _, e := range []struct {
			id, sid  string
			dayOfMon int
		}{
			{"e1", "s-old", 1},
			{"e2", "s-new", 10},
		} {
			eid, _ := types.EventIDFrom(e.id)
			agent, _ := types.AgentFrom("claude")
			sid, _ := types.SessionIDFrom(e.sid)
			ts := time.Date(2026, 4, e.dayOfMon, 12, 0, 0, 0, time.UTC)
			event := model.EventOf(eid, types.EventKindSessionStarted, "hook", agent, sid, "workspace", "start", ts)
			if err := fixture.eventDS.Save(ctx, event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			saveTestSession(ctx, t, fixture.sessionDS, e.sid, ts, types.None[time.Time](), "claude", "workspace")
		}

		toDate := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID(""), types.Workspace(""), types.Client(""), types.Agent(""), "", types.None[time.Time](), types.Some(toDate))
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("got %d summaries, want 1", len(summaries))
		}
		if diff := cmp.Diff("s-old", summaries[0].SessionID().String()); diff != "" {
			t.Fatalf("session mismatch (-want +got):\n%s", diff)
		}
	})

}
