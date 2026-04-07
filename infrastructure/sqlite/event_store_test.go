package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

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
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut := sqlite.NewDatasource(migrations)

	if err := sut.Initialize(context.Background(), dbPath); err != nil {
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

	if err := sut.Save(context.Background(), dbPath, olderEvent); err != nil {
		t.Fatalf("Save(older) error = %v", err)
	}
	if err := sut.Save(context.Background(), dbPath, newerEvent); err != nil {
		t.Fatalf("Save(newer) error = %v", err)
	}

	got, err := sut.ListRecent(context.Background(), dbPath, 10)
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

func TestDatasource_Initialize_既存DBへイベントメタデータ列を追加できる(t *testing.T) {
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
	if err := sqlite.NewDatasource(initialMigrations).Initialize(context.Background(), dbPath); err != nil {
		t.Fatalf("Initialize(initial) error = %v", err)
	}

	updatedMigrations := fstest.MapFS{
		"000001_init.sql": initialMigrations["000001_init.sql"],
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';`),
		},
	}
	sut := sqlite.NewDatasource(updatedMigrations)

	if err := sut.Initialize(context.Background(), dbPath); err != nil {
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
	if err := sut.Save(context.Background(), dbPath, event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := sut.ListRecent(context.Background(), dbPath, 1)
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
