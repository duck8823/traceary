package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
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
		"000014_add_session_spawn_metadata.sql": {
			Data: []byte(`ALTER TABLE sessions ADD COLUMN spawn_event_id TEXT;
ALTER TABLE sessions ADD COLUMN subagent_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN spawn_order INTEGER;
CREATE INDEX IF NOT EXISTS idx_sessions_parent_spawn_order
    ON sessions(parent_session_id, spawn_order);`),
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

func saveTestSessionWithParent(ctx context.Context, t *testing.T, ds *infra.SessionDatasource, sessionID string, parentID string, startedAt time.Time, order int) {
	t.Helper()
	saveTestSessionWithParentInWorkspace(ctx, t, ds, sessionID, parentID, startedAt, order, "duck8823/traceary")
}

func saveTestSessionWithParentInWorkspace(ctx context.Context, t *testing.T, ds *infra.SessionDatasource, sessionID string, parentID string, startedAt time.Time, order int, workspace string) {
	t.Helper()
	agent, _ := types.AgentFrom("codex")
	sid, _ := types.SessionIDFrom(sessionID)
	parentSID, _ := types.SessionIDFrom(parentID)
	spawnEventID, _ := types.EventIDFrom("spawn-" + sessionID)
	session := model.SessionOf(
		sid,
		startedAt,
		types.None[time.Time](),
		types.Client("hook"),
		agent,
		types.Workspace(workspace),
		"",
		"",
		parentSID,
		spawnEventID,
		"task",
		types.Some(order),
	)
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

		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID(""), types.Workspace(""), types.Client(""), types.Agent(""), "", false, types.None[time.Time](), types.None[time.Time]())
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

		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID(""), types.Workspace(""), types.Client(""), types.Agent("claude"), "", false, types.None[time.Time](), types.None[time.Time]())
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
		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID(""), types.Workspace(""), types.Client(""), types.Agent(""), "", false, types.Some(fromDate), types.None[time.Time]())
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

		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID("s1"), types.Workspace(""), types.Client(""), types.Agent(""), "", false, types.None[time.Time](), types.None[time.Time]())
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
		summaries, err := fixture.sessionDS.ListSummaries(ctx, 10, 0, types.SessionID(""), types.Workspace(""), types.Client(""), types.Agent(""), "", false, types.None[time.Time](), types.Some(toDate))
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

func TestDatasource_LineageOfCycleDetection(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := fixture.storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	saveTestSession(ctx, t, fixture.sessionDS, "self-cycle", started, types.None[time.Time](), "claude", "duck8823/traceary")
	updateTestSessionParent(ctx, t, dbPath, "self-cycle", "self-cycle")
	saveTestSession(ctx, t, fixture.sessionDS, "cycle-a", started, types.None[time.Time](), "claude", "duck8823/traceary")
	saveTestSessionWithParent(ctx, t, fixture.sessionDS, "cycle-b", "cycle-a", started.Add(time.Minute), 1)
	updateTestSessionParent(ctx, t, dbPath, "cycle-a", "cycle-b")

	lineageCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	selfGot, err := fixture.sessionDS.LineageOf(lineageCtx, types.SessionID("self-cycle"))
	if err != nil {
		t.Fatalf("LineageOf() self-cycle error = %v", err)
	}
	if len(selfGot) != 1 || selfGot[0].SessionID() != types.SessionID("self-cycle") {
		t.Fatalf("LineageOf() self-cycle = %+v, want self-cycle once", selfGot)
	}

	got, err := fixture.sessionDS.LineageOf(lineageCtx, types.SessionID("cycle-a"))
	if err != nil {
		t.Fatalf("LineageOf() error = %v", err)
	}
	gotIDs := make([]string, 0, len(got))
	for _, summary := range got {
		gotIDs = append(gotIDs, summary.SessionID().String())
	}
	sort.Strings(gotIDs)
	wantIDs := []string{"cycle-a", "cycle-b"}
	if diff := cmp.Diff(wantIDs, gotIDs); diff != "" {
		t.Fatalf("LineageOf() IDs mismatch (-want +got):\n%s", diff)
	}
}

type fakeClock struct {
	now time.Time
}

func (c fakeClock) Now() time.Time { return c.now }

func TestDatasource_ListTreeSummariesWithRootAppliesWorkspaceToDescendants(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := fixture.storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	saveTestSession(ctx, t, fixture.sessionDS, "parent", started, types.None[time.Time](), "codex", "workspace-a")
	saveTestSessionWithParentInWorkspace(ctx, t, fixture.sessionDS, "child-in-a", "parent", started.Add(time.Minute), 1, "workspace-a")
	saveTestSessionWithParentInWorkspace(ctx, t, fixture.sessionDS, "child-in-b", "parent", started.Add(2*time.Minute), 2, "workspace-b")

	got, err := fixture.sessionDS.ListTreeSummaries(ctx, 50, types.Workspace("workspace-a"), types.SessionID("parent"))
	if err != nil {
		t.Fatalf("ListTreeSummaries() error = %v", err)
	}
	gotIDs := make([]string, 0, len(got))
	for _, summary := range got {
		gotIDs = append(gotIDs, summary.SessionID().String())
	}
	if diff := cmp.Diff([]string{"parent", "child-in-a"}, gotIDs); diff != "" {
		t.Fatalf("ListTreeSummaries() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_ListTreeSummariesIncludesRequestedRootOutsideWorkspace(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := fixture.storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	saveTestSession(ctx, t, fixture.sessionDS, "parent", started, types.None[time.Time](), "codex", "workspace-b")
	saveTestSessionWithParentInWorkspace(ctx, t, fixture.sessionDS, "child-in-a", "parent", started.Add(time.Minute), 1, "workspace-a")
	saveTestSessionWithParentInWorkspace(ctx, t, fixture.sessionDS, "child-in-b", "parent", started.Add(2*time.Minute), 2, "workspace-b")

	got, err := fixture.sessionDS.ListTreeSummaries(ctx, 50, types.Workspace("workspace-a"), types.SessionID("parent"))
	if err != nil {
		t.Fatalf("ListTreeSummaries() error = %v", err)
	}
	gotIDs := make([]string, 0, len(got))
	for _, summary := range got {
		gotIDs = append(gotIDs, summary.SessionID().String())
	}
	if diff := cmp.Diff([]string{"parent", "child-in-a"}, gotIDs); diff != "" {
		t.Fatalf("ListTreeSummaries() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_ListTreeSummariesIncludesRequestedRootOutsideLimit(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := fixture.storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	saveTestSession(ctx, t, fixture.sessionDS, "old-root", started, types.None[time.Time](), "claude", "duck8823/traceary")
	for i := 0; i < 60; i++ {
		saveTestSession(ctx, t, fixture.sessionDS, fmt.Sprintf("newer-unrelated-%02d", i), started.Add(time.Duration(i+1)*time.Minute), types.None[time.Time](), "codex", "duck8823/traceary")
	}

	got, err := fixture.sessionDS.ListTreeSummaries(ctx, 50, types.Workspace("duck8823/traceary"), types.SessionID("old-root"))
	if err != nil {
		t.Fatalf("ListTreeSummaries() error = %v", err)
	}
	gotIDs := make([]string, 0, len(got))
	for _, summary := range got {
		gotIDs = append(gotIDs, summary.SessionID().String())
	}
	if diff := cmp.Diff([]string{"old-root"}, gotIDs); diff != "" {
		t.Fatalf("ListTreeSummaries() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_ListTreeSummariesWithRootIncludesDescendantsOutsideRecentLimit(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := fixture.storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	saveTestSession(ctx, t, fixture.sessionDS, "parent", started, types.None[time.Time](), "claude", "duck8823/traceary")
	for i := 0; i < 5; i++ {
		saveTestSessionWithParent(ctx, t, fixture.sessionDS, fmt.Sprintf("child-%02d", i+1), "parent", started.Add(time.Duration(i+1)*time.Minute), i+1)
	}
	for i := 0; i < 100; i++ {
		saveTestSession(ctx, t, fixture.sessionDS, fmt.Sprintf("newer-unrelated-%03d", i), started.Add(time.Duration(i+1)*time.Hour), types.None[time.Time](), "codex", "duck8823/traceary")
	}

	got, err := fixture.sessionDS.ListTreeSummaries(ctx, 3, types.Workspace("duck8823/traceary"), types.SessionID("parent"))
	if err != nil {
		t.Fatalf("ListTreeSummaries() error = %v", err)
	}
	gotIDs := make([]string, 0, len(got))
	for _, summary := range got {
		gotIDs = append(gotIDs, summary.SessionID().String())
	}
	wantIDs := []string{"parent", "child-01", "child-02", "child-03", "child-04", "child-05"}
	if diff := cmp.Diff(wantIDs, gotIDs); diff != "" {
		t.Fatalf("ListTreeSummaries() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_RejectsSelfParentSession(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := fixture.storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	agent, _ := types.AgentFrom("codex")
	sid, _ := types.SessionIDFrom("self-parent")
	session := model.SessionOf(
		sid,
		time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		types.None[time.Time](),
		types.Client("cli"),
		agent,
		types.Workspace("duck8823/traceary"),
		"",
		"",
		sid,
	)

	err := fixture.sessionDS.SaveSessionBoundaryForTest(ctx, session)
	if err == nil {
		t.Fatalf("SaveSessionBoundaryForTest() error = nil, want self-parent rejection")
	}
	if !strings.Contains(err.Error(), "itself as parent") {
		t.Fatalf("SaveSessionBoundaryForTest() error = %v, want self-parent rejection", err)
	}
}

func updateTestSessionParent(ctx context.Context, t *testing.T, dbPath string, sessionID string, parentID string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "UPDATE sessions SET parent_session_id = ? WHERE session_id = ?", parentID, sessionID); err != nil {
		t.Fatalf("UPDATE sessions parent_session_id error = %v", err)
	}
}

func TestDatasource_LineageOf(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	fixture := newListSessionsFixture(t, dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := fixture.storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	saveTestSession(ctx, t, fixture.sessionDS, "root", started, types.None[time.Time](), "claude", "duck8823/traceary")
	saveTestSessionWithParent(ctx, t, fixture.sessionDS, "child-2", "root", started, 2)
	saveTestSessionWithParent(ctx, t, fixture.sessionDS, "child-1", "root", started, 1)
	saveTestSessionWithParent(ctx, t, fixture.sessionDS, "grandchild", "child-1", started.Add(time.Minute), 1)
	saveTestSession(ctx, t, fixture.sessionDS, "unrelated", started, types.None[time.Time](), "claude", "duck8823/traceary")

	got, err := fixture.sessionDS.LineageOf(ctx, types.SessionID("child-1"))
	if err != nil {
		t.Fatalf("LineageOf() error = %v", err)
	}
	gotIDs := make([]string, 0, len(got))
	for _, summary := range got {
		gotIDs = append(gotIDs, summary.SessionID().String())
	}
	wantIDs := []string{"root", "child-1", "child-2", "grandchild"}
	gotIDSet := append([]string(nil), gotIDs...)
	sort.Strings(gotIDSet)
	wantIDSet := append([]string(nil), wantIDs...)
	sort.Strings(wantIDSet)
	if diff := cmp.Diff(wantIDSet, gotIDSet); diff != "" {
		t.Fatalf("LineageOf() IDs mismatch (-want +got):\n%s", diff)
	}
	childOneIndex := slices.Index(gotIDs, "child-1")
	childTwoIndex := slices.Index(gotIDs, "child-2")
	if childOneIndex < 0 || childTwoIndex < 0 || childOneIndex > childTwoIndex {
		t.Fatalf("siblings were not ordered by spawn_order: %v", gotIDs)
	}
	spawnOrder, ok := got[childOneIndex].SpawnOrder().Value()
	if !ok || spawnOrder != 1 {
		t.Fatalf("child-1 spawn_order = (%d, %v), want (1, true)", spawnOrder, ok)
	}
}
