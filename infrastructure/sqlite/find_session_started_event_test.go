package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestDatasource_FindSessionStartedEvent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut := NewDatasource(dbPath, fstest.MapFS{
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
	})
	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	started := newFindSessionStartedEventFixture(
		t,
		"event-started",
		types.EventKindSessionStarted,
		"hook",
		"codex",
		"session-1",
		"repo-1",
		"session started",
		time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
	)
	ended := newFindSessionStartedEventFixture(
		t,
		"event-ended",
		types.EventKindSessionEnded,
		"hook",
		"codex",
		"session-1",
		"repo-1",
		"session ended",
		time.Date(2026, 4, 8, 12, 5, 0, 0, time.UTC),
	)
	newerStarted := newFindSessionStartedEventFixture(
		t,
		"event-started-2",
		types.EventKindSessionStarted,
		"cli",
		"claude",
		"session-1",
		"repo-2",
		"session started",
		time.Date(2026, 4, 8, 12, 10, 0, 0, time.UTC),
	)
	otherSession := newFindSessionStartedEventFixture(
		t,
		"event-started-other",
		types.EventKindSessionStarted,
		"cli",
		"gemini",
		"session-2",
		"repo-3",
		"session started",
		time.Date(2026, 4, 8, 12, 20, 0, 0, time.UTC),
	)
	fixtures := []*model.Event{started, ended, newerStarted, otherSession}
	for _, fixture := range fixtures {
		if err := sut.Save(context.Background(), fixture); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	t.Run("returns latest session_started for target session", func(t *testing.T) {
		t.Parallel()

		sessionID, err := types.SessionIDOf("session-1")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}

		got, err := sut.FindSessionStartedEvent(context.Background(), sessionID)
		if err != nil {
			t.Fatalf("FindSessionStartedEvent() error = %v", err)
		}
		if got.EventID().String() != "event-started-2" {
			t.Fatalf("EventID() = %q, want %q", got.EventID(), "event-started-2")
		}
		if got.Client() != "cli" {
			t.Fatalf("Client() = %q, want %q", got.Client(), "cli")
		}
		if got.Agent().String() != "claude" {
			t.Fatalf("Agent() = %q, want %q", got.Agent(), "claude")
		}
		if got.Workspace() != "repo-2" {
			t.Fatalf("Repo() = %q, want %q", got.Workspace(), "repo-2")
		}
	})

	t.Run("returns not found when no match exists", func(t *testing.T) {
		t.Parallel()

		sessionID, err := types.SessionIDOf("session-missing")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}

		_, err = sut.FindSessionStartedEvent(context.Background(), sessionID)
		if !errors.Is(err, usecase.ErrSessionStartedEventNotFound) {
			t.Fatalf("error = %v, want ErrSessionStartedEventNotFound", err)
		}
	})
}

func newFindSessionStartedEventFixture(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
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

	return model.EventOf(eventID, kind, client, agent, sessionID, workspace, body, createdAt)
}
