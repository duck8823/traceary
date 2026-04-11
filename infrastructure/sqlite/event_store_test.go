package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/domain/port"
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
ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';

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
	sut := sqlite.NewDatasource(dbPath, migrations)

	if err := sut.Initialize(context.Background()); err != nil {
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

	got, err := sut.ListRecent(context.Background(), port.ListRecentEventsInput{
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(got))
	}
	if got[0].EventID().String() != "event-2" {
		t.Fatalf("got[0].EventID() = %q, want %q", got[0].EventID(), "event-2")
	}
	if got[0].Client() != "hook" {
		t.Fatalf("got[0].Client() = %q, want %q", got[0].Client(), "hook")
	}
	if got[1].Repo() != "duck8823/traceary" {
		t.Fatalf("got[1].Repo() = %q, want %q", got[1].Repo(), "duck8823/traceary")
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
	if err := sqlite.NewDatasource(dbPath, initialMigrations).Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(initial) error = %v", err)
	}

	updatedMigrations := fstest.MapFS{
		"000001_init.sql": initialMigrations["000001_init.sql"],
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';`),
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
	sut := sqlite.NewDatasource(dbPath, updatedMigrations)

	if err := sut.Initialize(context.Background()); err != nil {
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

	got, err := sut.ListRecent(context.Background(), port.ListRecentEventsInput{
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if got[0].Client() != "cli" {
		t.Fatalf("Client() = %q, want %q", got[0].Client(), "cli")
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
ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';

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
	sut := sqlite.NewDatasource(dbPath, migrations)

	if err := sut.Initialize(context.Background()); err != nil {
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

	got, err := sut.ListRecent(context.Background(), port.ListRecentEventsInput{
		Limit:  1,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if got[0].EventID().String() != "event-2" {
		t.Fatalf("got[0].EventID() = %q, want %q", got[0].EventID(), "event-2")
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
ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';

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
	sut := sqlite.NewDatasource(dbPath, migrations)

	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	firstEventID := mustEventIDForSQLite(t, "event-note")
	secondEventID := mustEventIDForSQLite(t, "event-command")
	codexAgent := mustAgentForSQLite(t, "codex")
	claudeAgent := mustAgentForSQLite(t, "claude")
	sessionOne := mustSessionIDForSQLite(t, "session-1")
	sessionTwo := mustSessionIDForSQLite(t, "session-2")

	events := []*model.Event{
		model.EventOf(firstEventID, types.EventKindNote, "cli", codexAgent, sessionOne, "duck8823/traceary", "first", time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)),
		model.EventOf(secondEventID, types.EventKindCommandExecuted, "hook", claudeAgent, sessionTwo, "other/repo", "second", time.Date(2026, 4, 7, 12, 1, 0, 0, time.UTC)),
	}
	for _, event := range events {
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", event.EventID(), err)
		}
	}

	got, err := sut.ListRecent(context.Background(), port.ListRecentEventsInput{
		Limit:     10,
		Kind:      types.EventKindNote.String(),
		Client:    "cli",
		Agent:     "codex",
		SessionID: "session-1",
		Repo:      "duck8823/traceary",
	})
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if got[0].EventID().String() != "event-note" {
		t.Fatalf("got[0].EventID() = %q, want %q", got[0].EventID(), "event-note")
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
	repo string,
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
		client,
		agent,
		sessionID,
		repo,
		body,
		createdAt,
	)
}
