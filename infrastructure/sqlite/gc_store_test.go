package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_CollectGarbage_DryRun(t *testing.T) {
	t.Parallel()

	dbPath, fixture := prepareGCFixture(t)

	deletedCount, err := fixture.storeManager.CollectGarbage(
		context.Background(),
		time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
		true,
	)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(1, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(2, countEvents(t, dbPath)); diff != "" {
		t.Fatalf("event count mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_CollectGarbage_deletesOldEventsAndAudits(t *testing.T) {
	t.Parallel()

	dbPath, fixture := prepareGCFixture(t)

	deletedCount, err := fixture.storeManager.CollectGarbage(
		context.Background(),
		time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
		false,
	)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(1, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(1, countEvents(t, dbPath)); diff != "" {
		t.Fatalf("event count mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, countCommandAudits(t, dbPath)); diff != "" {
		t.Fatalf("command audit count mismatch (-want +got):\n%s", diff)
	}
}

type gcFixture struct {
	eventDS      *sqlite.EventDatasource
	storeManager *sqlite.StoreManagementDatasource
}

func prepareGCFixture(t *testing.T) (string, *gcFixture) {
	t.Helper()

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
	eventDS, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	oldAuditEvent, oldCommandAudit := newOldAuditFixture(t)
	if err := eventDS.SaveWithAudit(context.Background(), oldAuditEvent, oldCommandAudit); err != nil {
		t.Fatalf("SaveWithAudit(old) error = %v", err)
	}
	newNoteEvent := newGCEventFixture(
		t,
		"event-new",
		types.EventKindNote,
		"recent note",
		time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC),
	)
	if err := eventDS.Save(context.Background(), newNoteEvent); err != nil {
		t.Fatalf("Save(new) error = %v", err)
	}

	return dbPath, &gcFixture{eventDS: eventDS, storeManager: storeManager}
}

func newOldAuditFixture(t *testing.T) (*model.Event, *model.CommandAudit) {
	t.Helper()

	event := newGCEventFixture(
		t,
		"event-old",
		types.EventKindCommandExecuted,
		"go test ./...",
		time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
	)
	commandAudit, err := model.NewCommandAudit(
		event.EventID(),
		"go test ./...",
		"stdin",
		"stdout",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	return event, commandAudit
}

func newGCEventFixture(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
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
		"duck8823/traceary",
		body,
		createdAt,
	)
}

func countEvents(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("event count query error = %v", err)
	}

	return count
}

func countCommandAudits(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM command_audits`).Scan(&count); err != nil {
		t.Fatalf("command_audits count query error = %v", err)
	}

	return count
}
