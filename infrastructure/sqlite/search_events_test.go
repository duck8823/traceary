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
)

func TestDatasource_Search(t *testing.T) {
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
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
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
	if err := sut.Save(context.Background(), noteEvent); err != nil {
		t.Fatalf("Save(note) error = %v", err)
	}

	auditEvent, commandAudit := newSearchAuditFixture(
		t,
		"event-audit",
		"github.com/duck8823/traceary",
		time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.SaveWithAudit(context.Background(), auditEvent, commandAudit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	got, err := sut.Search(context.Background(), "stdout", types.Workspace("github.com/duck8823/traceary"), types.SessionID(""), types.Client(""), types.Agent(""), types.EventKind(""), time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC), 10, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if diff := cmp.Diff("event-audit", got[0].EventID().String()); diff != "" {
		t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
	}

	t.Run("searches with structural filters only", func(t *testing.T) {
		t.Parallel()

		filtered, err := sut.Search(context.Background(), "", types.Workspace("github.com/duck8823/traceary"), types.SessionID("session-1"), types.Client("cli"), types.Agent("codex"), types.EventKind("note"), time.Time{}, time.Time{}, 10, 0, false)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("len(filtered) = %d, want 1", len(filtered))
		}
		if diff := cmp.Diff("event-note", filtered[0].EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("retrieves second page with offset", func(t *testing.T) {
		t.Parallel()

		filtered, err := sut.Search(context.Background(), "", types.Workspace("github.com/duck8823/traceary"), types.SessionID(""), types.Client(""), types.Agent(""), types.EventKind(""), time.Time{}, time.Time{}, 1, 1, false)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("len(filtered) = %d, want 1", len(filtered))
		}
		if diff := cmp.Diff("event-note", filtered[0].EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})
}

func newSearchEventFixture(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
	workspace string,
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
		workspace,
		body,
		createdAt,
	)
}

func newSearchAuditFixture(
	t *testing.T,
	eventIDValue string,
	workspace string,
	createdAt time.Time,
) (*model.Event, *model.CommandAudit) {
	t.Helper()

	event := newSearchEventFixture(
		t,
		eventIDValue,
		types.EventKindCommandExecuted,
		workspace,
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
