package sqlite_test

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_SaveAndListRecent(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_events_created_at
    ON events(created_at DESC, id DESC);`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)

	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	olderEvent := newEventForSQLiteTest(
		t,
		"event-1",
		"cli",
		"codex",
		"session-1",
		"duck8823/traceary",
		"first",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)
	newerEvent := newEventForSQLiteTest(
		t,
		"event-2",
		"hook",
		"claude",
		"session-2",
		"",
		"second",
		time.Date(2026, 4, 7, 12, 30, 0, 0, time.UTC),
	)

	if err := sut.Save(context.Background(), olderEvent); err != nil {
		t.Fatalf("Save(older) error = %v", err)
	}
	if err := sut.Save(context.Background(), newerEvent); err != nil {
		t.Fatalf("Save(newer) error = %v", err)
	}

	got, err := sut.ListRecent(context.Background(), 10, 0, types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""), false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(got))
	}
	if diff := cmp.Diff("event-2", got[0].EventID().String()); diff != "" {
		t.Fatalf("got[0].EventID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.Client("hook"), got[0].Client()); diff != "" {
		t.Fatalf("got[0].Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.Workspace("duck8823/traceary"), got[1].Workspace()); diff != "" {
		t.Fatalf("got[1].Workspace() mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_Initialize_addsEventMetadataColumnsToExistingDB(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")

	initialMigrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
	}
	initialDB := sqlite.NewDatabase(dbPath, initialMigrations)
	if err := sqlite.NewStoreManagementDatasource(initialDB).Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(initial) error = %v", err)
	}

	updatedMigrations := fstest.MapFS{
		"000001_init.sql": initialMigrations["000001_init.sql"],
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER
);`),
		},
	}
	sut, storeManager := newEventDatasource(t, dbPath, updatedMigrations)

	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(updated) error = %v", err)
	}

	event := newEventForSQLiteTest(
		t,
		"event-1",
		"cli",
		"codex",
		"session-1",
		"duck8823/traceary",
		"hello",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := sut.ListRecent(context.Background(), 1, 0, types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""), false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if diff := cmp.Diff(types.Client("cli"), got[0].Client()); diff != "" {
		t.Fatalf("Client() mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_ListRecent_Offset(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_events_created_at
    ON events(created_at DESC, id DESC);`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)

	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	for index, eventID := range []string{"event-1", "event-2", "event-3"} {
		event := newEventForSQLiteTest(
			t,
			eventID,
			"cli",
			"codex",
			"session-1",
			"duck8823/traceary",
			eventID,
			time.Date(2026, 4, 7, 12, index, 0, 0, time.UTC),
		)
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", eventID, err)
		}
	}

	got, err := sut.ListRecent(context.Background(), 1, 1, types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""), false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if diff := cmp.Diff("event-2", got[0].EventID().String()); diff != "" {
		t.Fatalf("got[0].EventID() mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_ListRecent_Filters(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_events_created_at
    ON events(created_at DESC, id DESC);`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)

	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	firstEventID := mustEventIDForSQLite(t, "event-note")
	secondEventID := mustEventIDForSQLite(t, "event-command")
	codexAgent := mustAgentForSQLite(t, "codex")
	claudeAgent := mustAgentForSQLite(t, "claude")
	sessionOne := mustSessionIDForSQLite(t, "session-1")
	sessionTwo := mustSessionIDForSQLite(t, "session-2")

	events := []*model.Event{
		model.EventOf(firstEventID, types.EventKindNote, types.Client("cli"), codexAgent, sessionOne, types.Workspace("duck8823/traceary"), "first", time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)),
		model.EventOf(secondEventID, types.EventKindCommandExecuted, types.Client("hook"), claudeAgent, sessionTwo, types.Workspace("other/workspace"), "second", time.Date(2026, 4, 7, 12, 1, 0, 0, time.UTC)),
	}
	for _, event := range events {
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", event.EventID(), err)
		}
	}

	got, err := sut.ListRecent(context.Background(), 10, 0, types.EventKindNote, types.Client("cli"), types.Agent("codex"), types.SessionID("session-1"), types.Workspace("duck8823/traceary"), false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if diff := cmp.Diff("event-note", got[0].EventID().String()); diff != "" {
		t.Fatalf("got[0].EventID() mismatch (-want +got):\n%s", diff)
	}
}

func mustEventIDForSQLite(t *testing.T, value string) types.EventID {
	t.Helper()

	eventID, err := types.EventIDOf(value)
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}

	return eventID
}

func mustAgentForSQLite(t *testing.T, value string) types.Agent {
	t.Helper()

	agent, err := types.AgentOf(value)
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}

	return agent
}

func mustSessionIDForSQLite(t *testing.T, value string) types.SessionID {
	t.Helper()

	sessionID, err := types.SessionIDOf(value)
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	return sessionID
}

func newEventForSQLiteTest(
	t *testing.T,
	eventIDValue string,
	client string,
	agentValue string,
	sessionIDValue string,
	workspace string,
	body string,
	createdAt time.Time,
) *model.Event {
	t.Helper()

	eventID, err := types.EventIDOf(eventIDValue)
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf(agentValue)
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf(sessionIDValue)
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	return model.EventOf(
		eventID,
		types.EventKindNote,
		types.Client(client),
		agent,
		sessionID,
		types.Workspace(workspace),
		body,
		createdAt,
	)
}

func TestDatasource_ListWindow_ReturnsAllEventsAcrossBatches(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_events_created_at
    ON events(created_at DESC, id DESC);`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)

	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	windowStart := time.Date(2026, 4, 13, 18, 0, 0, 0, time.UTC)
	// Insert 250 events so the paged scan has to issue at least three batches
	// when criteria.Limit() is 100. Before the ListWindow fix, the tail poller
	// used OFFSET-based pagination across separate connections and would drop
	// rows when a concurrent writer interleaved inserts between pages; this
	// test guards the fixed, transaction-scoped loop against regressions in
	// page assembly for large windows.
	totalEvents := 250
	for i := range totalEvents {
		event := newEventForSQLiteTest(
			t,
			"event-"+strconv.Itoa(i),
			"cli",
			"codex",
			"session-1",
			"duck8823/traceary",
			"body",
			windowStart.Add(time.Duration(i)*time.Second),
		)
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(event-%d) error = %v", i, err)
		}
	}

	criteria := apptypes.NewEventListCriteriaBuilder(100).
		From(windowStart).
		To(windowStart.Add(time.Duration(totalEvents+1) * time.Second)).
		Build()

	got, err := sut.ListWindow(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ListWindow() error = %v", err)
	}
	if len(got) != totalEvents {
		t.Fatalf("len(events) = %d, want %d", len(got), totalEvents)
	}
	// DESC order: events[0] is newest.
	if got[0].EventID().String() != "event-"+strconv.Itoa(totalEvents-1) {
		t.Fatalf("got[0].EventID() = %q, want %q", got[0].EventID().String(), "event-"+strconv.Itoa(totalEvents-1))
	}
	if got[totalEvents-1].EventID().String() != "event-0" {
		t.Fatalf("got[last].EventID() = %q, want event-0", got[totalEvents-1].EventID().String())
	}
	seen := make(map[string]struct{}, totalEvents)
	for _, event := range got {
		id := event.EventID().String()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate event in window result: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestDatasource_ListWindow_RespectsToUpperBoundExclusive(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_events_created_at
    ON events(created_at DESC, id DESC);`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)

	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 4, 13, 18, 0, 0, 0, time.UTC)
	inside := newEventForSQLiteTest(t, "event-inside", "cli", "codex", "session-1", "ws", "in", base)
	boundary := newEventForSQLiteTest(t, "event-boundary", "cli", "codex", "session-1", "ws", "edge", base.Add(5*time.Second))
	if err := sut.Save(context.Background(), inside); err != nil {
		t.Fatalf("Save(inside) error = %v", err)
	}
	if err := sut.Save(context.Background(), boundary); err != nil {
		t.Fatalf("Save(boundary) error = %v", err)
	}

	// To is exclusive: an event whose created_at equals To must be excluded.
	criteria := apptypes.NewEventListCriteriaBuilder(10).
		From(base).
		To(base.Add(5 * time.Second)).
		Build()

	got, err := sut.ListWindow(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ListWindow() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1 (boundary event must be excluded)", len(got))
	}
	if got[0].EventID().String() != "event-inside" {
		t.Fatalf("got[0].EventID() = %q, want event-inside", got[0].EventID().String())
	}
}
