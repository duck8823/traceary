package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_CollectGarbage_DryRun(t *testing.T) {
	t.Parallel()

	dbPath, sut := prepareGCFixture(t)

	deletedCount, err := sut.CollectGarbage(
		context.Background(),
		time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
		true,
	)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if deletedCount != 1 {
		t.Fatalf("deletedCount = %d, want 1", deletedCount)
	}

	if got := countEvents(t, dbPath); got != 2 {
		t.Fatalf("event count = %d, want 2", got)
	}
}

func TestDatasource_CollectGarbage_古いイベントと監査情報を削除する(t *testing.T) {
	t.Parallel()

	dbPath, sut := prepareGCFixture(t)

	deletedCount, err := sut.CollectGarbage(
		context.Background(),
		time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
		false,
	)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if deletedCount != 1 {
		t.Fatalf("deletedCount = %d, want 1", deletedCount)
	}

	if got := countEvents(t, dbPath); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
	if got := countCommandAudits(t, dbPath); got != 0 {
		t.Fatalf("command audit count = %d, want 0", got)
	}
}

func prepareGCFixture(t *testing.T) (string, *sqlite.Datasource) {
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
	sut := sqlite.NewDatasource(dbPath, migrations)
	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	oldAuditEvent, oldCommandAudit := newOldAuditFixture(t)
	if err := sut.SaveWithAudit(context.Background(), oldAuditEvent, oldCommandAudit); err != nil {
		t.Fatalf("SaveWithAudit(old) error = %v", err)
	}
	newNoteEvent := newGCEventFixture(
		t,
		"event-new",
		types.EventKindNote,
		"recent note",
		time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), newNoteEvent); err != nil {
		t.Fatalf("Save(new) error = %v", err)
	}

	return dbPath, sut
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

	db, err := sql.Open("sqlite", "file:" + dbPath)
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

	db, err := sql.Open("sqlite", "file:" + dbPath)
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
