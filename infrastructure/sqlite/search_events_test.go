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
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_SearchEvents(t *testing.T) {
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
    output_truncated INTEGER NOT NULL DEFAULT 0
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut := sqlite.NewDatasource(migrations)
	if err := sut.Initialize(context.Background(), dbPath); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	noteEvent := newSearchEventFixture(
		t,
		"event-note",
		types.EventKindNote,
		"github.com/duck8823/traceary",
		"hello traceary",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, noteEvent); err != nil {
		t.Fatalf("Save(note) error = %v", err)
	}

	auditEvent, commandAudit := newSearchAuditFixture(
		t,
		"event-audit",
		"github.com/duck8823/traceary",
		time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.SaveCommandAudit(context.Background(), dbPath, auditEvent, commandAudit); err != nil {
		t.Fatalf("SaveCommandAudit() error = %v", err)
	}

	got, err := sut.SearchEvents(context.Background(), dbPath, queryservice.SearchEventsInput{
		Query: "stdout",
		Repo:  "github.com/duck8823/traceary",
		From:  time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC),
		To:    time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC),
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchEvents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if got[0].EventID().String() != "event-audit" {
		t.Fatalf("EventID() = %q, want %q", got[0].EventID(), "event-audit")
	}

	t.Run("searches with structural filters only", func(t *testing.T) {
		t.Parallel()

		filtered, err := sut.SearchEvents(context.Background(), dbPath, queryservice.SearchEventsInput{
			Repo:      "github.com/duck8823/traceary",
			SessionID: "session-1",
			Client:    "cli",
			Agent:     "codex",
			Kind:      "note",
			Limit:     10,
		})
		if err != nil {
			t.Fatalf("SearchEvents() error = %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("len(filtered) = %d, want 1", len(filtered))
		}
		if filtered[0].EventID().String() != "event-note" {
			t.Fatalf("EventID() = %q, want %q", filtered[0].EventID(), "event-note")
		}
	})

	t.Run("offset で 2 ページ目を取得できる", func(t *testing.T) {
		t.Parallel()

		filtered, err := sut.SearchEvents(context.Background(), dbPath, queryservice.SearchEventsInput{
			Repo:   "github.com/duck8823/traceary",
			Limit:  1,
			Offset: 1,
		})
		if err != nil {
			t.Fatalf("SearchEvents() error = %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("len(filtered) = %d, want 1", len(filtered))
		}
		if filtered[0].EventID().String() != "event-note" {
			t.Fatalf("EventID() = %q, want %q", filtered[0].EventID(), "event-note")
		}
	})
}

func newSearchEventFixture(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
	repo string,
	body string,
	createdAt time.Time,
) *model.Event {
	t.Helper()

	eventID, err := types.EventIDOf(eventIDValue)
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-1")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	return model.EventOf(
		eventID,
		kind,
		"cli",
		agent,
		sessionID,
		repo,
		body,
		createdAt,
	)
}

func newSearchAuditFixture(
	t *testing.T,
	eventIDValue string,
	repo string,
	createdAt time.Time,
) (*model.Event, *model.CommandAudit) {
	t.Helper()

	event := newSearchEventFixture(
		t,
		eventIDValue,
		types.EventKindCommandExecuted,
		repo,
		"go test ./...",
		createdAt,
	)
	commandAudit, err := model.NewCommandAudit(
		event.EventID(),
		"go test ./...",
		"stdin",
		"stdout with details",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	return event, commandAudit
}
