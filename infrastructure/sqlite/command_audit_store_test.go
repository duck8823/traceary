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

func TestDatasource_SaveCommandAudit(t *testing.T) {
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
	sut := sqlite.NewDatasource(dbPath, migrations)
	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	eventID, err := types.EventIDOf("event-1")
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
	event := model.EventOf(
		eventID,
		types.EventKindCommandExecuted,
		"cli",
		agent,
		sessionID,
		"duck8823/traceary",
		"go test ./...",
		time.Date(2026, 4, 7, 14, 0, 0, 0, time.UTC),
	)
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"go test ./...",
		"stdin",
		"stdout",
		true,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	if err := sut.SaveCommandAudit(context.Background(), event, commandAudit); err != nil {
		t.Fatalf("SaveCommandAudit() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var (
		kind            string
		commandText     string
		inputTruncated  bool
		outputTruncated bool
	)
	if err := db.QueryRow(`
SELECT e.kind, a.command_text, a.input_truncated, a.output_truncated
  FROM command_audits a
  JOIN events e ON e.id = a.event_id
 WHERE a.event_id = ?`,
		"event-1",
	).Scan(&kind, &commandText, &inputTruncated, &outputTruncated); err != nil {
		t.Fatalf("audit query error = %v", err)
	}
	if kind != "command_executed" {
		t.Fatalf("kind = %q, want %q", kind, "command_executed")
	}
	if commandText != "go test ./..." {
		t.Fatalf("command_text = %q, want %q", commandText, "go test ./...")
	}
	if !inputTruncated {
		t.Fatalf("input_truncated = false, want true")
	}
	if outputTruncated {
		t.Fatalf("output_truncated = true, want false")
	}
}
