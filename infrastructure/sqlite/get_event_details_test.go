package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/types"
)

func TestDatasource_GetDetails(t *testing.T) {
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
    body_availability TEXT NOT NULL DEFAULT 'available',
    created_at TEXT NOT NULL,
    source_hook TEXT
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
    command_wrapper TEXT NOT NULL DEFAULT '',
    command_name TEXT NOT NULL DEFAULT 'unknown',
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    input_original_bytes INTEGER NOT NULL DEFAULT 0,
    output_original_bytes INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER,
    failed INTEGER NOT NULL DEFAULT 0,
    failure_reason TEXT NOT NULL DEFAULT 'unknown'
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	event, commandAudit := newSearchAuditFixture(
		t,
		"event-audit",
		"github.com/duck8823/traceary",
		time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.SaveWithAudit(context.Background(), event, commandAudit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	t.Run("returns event and command audit", func(t *testing.T) {
		t.Parallel()

		got, err := sut.GetDetails(context.Background(), types.EventID("event-audit"))
		if err != nil {
			t.Fatalf("GetDetails() error = %v", err)
		}
		if diff := cmp.Diff("event-audit", got.Event().EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
		if _, ok := got.CommandAudit().Value(); !ok {
			t.Fatalf("CommandAudit() is empty, want command audit")
		}
		audit, _ := got.CommandAudit().Value()
		if diff := cmp.Diff("stdout with details", audit.Output()); diff != "" {
			t.Fatalf("Output() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("go", audit.CommandIdentity().Command().String()); diff != "" {
			t.Fatalf("CommandIdentity().Command() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("unknown", audit.FailureReason().String()); diff != "" {
			t.Fatalf("FailureReason() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns error for nonexistent event ID", func(t *testing.T) {
		t.Parallel()

		_, err := sut.GetDetails(context.Background(), types.EventID("missing"))
		if err == nil {
			t.Fatalf("GetDetails() error = nil, want error")
		}
	})
}
