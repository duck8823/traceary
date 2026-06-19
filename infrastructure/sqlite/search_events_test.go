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
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    input_original_bytes INTEGER NOT NULL DEFAULT 0,
    output_original_bytes INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER,
    failed INTEGER NOT NULL DEFAULT 0
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

	pathEventID, _ := types.EventIDFrom("event-path")
	pathAgent, _ := types.AgentFrom("codex")
	pathSessionID, _ := types.SessionIDFrom("session-path")
	pathEvent := model.EventOf(
		pathEventID,
		types.EventKindNote,
		types.Client("cli"),
		pathAgent,
		pathSessionID,
		types.Workspace("github.com/duck8823/traceary"),
		`Windows path C:\traceary\logs`,
		time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), pathEvent); err != nil {
		t.Fatalf("Save(pathEvent) error = %v", err)
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

	t.Run("matches literal backslashes in search queries", func(t *testing.T) {
		t.Parallel()

		filtered, err := sut.Search(context.Background(), `C:\traceary\logs`, types.Workspace("github.com/duck8823/traceary"), types.SessionID(""), types.Client(""), types.Agent(""), types.EventKind(""), time.Time{}, time.Time{}, 10, 0, false)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("len(filtered) = %d, want 1", len(filtered))
		}
		if diff := cmp.Diff("event-path", filtered[0].EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("matches literal backslashes in search queries", func(t *testing.T) {
		t.Parallel()

		filtered, err := sut.Search(context.Background(), `C:\traceary\logs`, types.Workspace("github.com/duck8823/traceary"), types.SessionID(""), types.Client(""), types.Agent(""), types.EventKind(""), time.Time{}, time.Time{}, 10, 0, false)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("len(filtered) = %d, want 1", len(filtered))
		}
		if diff := cmp.Diff("event-path", filtered[0].EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestDatasource_Search_SkipsThinkingOnlyMatches(t *testing.T) {
	t.Parallel()

	migrations := searchEventsMigrations()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	ws := "github.com/duck8823/traceary"
	thinkingOnly := newSearchEventFixture(
		t,
		"event-thinking-only",
		types.EventKindTranscript,
		ws,
		`{"blocks":[{"type":"thinking","text":"secret-needle reasoning"}]}`,
		time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), thinkingOnly); err != nil {
		t.Fatalf("Save(thinkingOnly) error = %v", err)
	}

	mixed := newSearchEventFixture(
		t,
		"event-mixed",
		types.EventKindTranscript,
		ws,
		`{"blocks":[{"type":"thinking","text":"ignore me"},{"type":"text","text":"secret-needle visible"}]}`,
		time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), mixed); err != nil {
		t.Fatalf("Save(mixed) error = %v", err)
	}

	filtered, err := sut.Search(context.Background(), "secret-needle", types.Workspace(ws), types.SessionID(""), types.Client(""), types.Agent(""), types.EventKind(""), time.Time{}, time.Time{}, 10, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("len(filtered) = %d, want 1 (mixed-block row only; thinking-only row must be filtered out)", len(filtered))
	}
	if diff := cmp.Diff("event-mixed", filtered[0].EventID().String()); diff != "" {
		t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_Search_PreservesNonCanonicalBlocksBody(t *testing.T) {
	t.Parallel()

	// Non-canonical envelope: has a "blocks" key but elements do not
	// match the {type:string, text:string} shape. ExtractPlainBody
	// returns the raw body for these, so search must keep the raw body
	// searchable too — otherwise the search surface and the display
	// surface disagree.
	migrations := searchEventsMigrations()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	ws := "github.com/duck8823/traceary"
	cases := []struct {
		name string
		id   string
		body string
	}{
		{"element missing type/text", "event-foreign-shape", `{"blocks":[{"foo":"non-canonical-needle"}]}`},
		{"text field is non-string", "event-nonstring-text", `{"blocks":[{"type":"text","text":42}]}`},
		{"type field is non-string", "event-nonstring-type", `{"blocks":[{"type":42,"text":"non-canonical-needle"}]}`},
		{"blocks:null falls through", "event-blocks-null", `{"blocks":null,"note":"non-canonical-needle"}`},
	}
	for i, c := range cases {
		fixture := newSearchEventFixture(
			t,
			c.id,
			types.EventKindNote,
			ws,
			c.body,
			time.Date(2026, 4, 15, 12, i, 0, 0, time.UTC),
		)
		if err := sut.Save(context.Background(), fixture); err != nil {
			t.Fatalf("Save(%s) error = %v", c.name, err)
		}
	}

	filtered, err := sut.Search(context.Background(), "non-canonical-needle", types.Workspace(ws), types.SessionID(""), types.Client(""), types.Agent(""), types.EventKind(""), time.Time{}, time.Time{}, 10, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	// All 3 bodies that contain the needle literal in their raw JSON
	// must match (the nonstring-text case does not contain the string
	// "non-canonical-needle", so we expect 3 hits, not 4).
	if len(filtered) != 3 {
		t.Fatalf("len(filtered) = %d, want 3 (non-canonical envelopes must remain raw-searchable)", len(filtered))
	}
}

func TestDatasource_Search_PreservesNonEnvelopeJSONBody(t *testing.T) {
	t.Parallel()

	migrations := searchEventsMigrations()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	ws := "github.com/duck8823/traceary"
	nonEnvelope := newSearchEventFixture(
		t,
		"event-non-envelope",
		types.EventKindNote,
		ws,
		`{"foo":"unique-json-marker"}`,
		time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), nonEnvelope); err != nil {
		t.Fatalf("Save(nonEnvelope) error = %v", err)
	}

	filtered, err := sut.Search(context.Background(), "unique-json-marker", types.Workspace(ws), types.SessionID(""), types.Client(""), types.Agent(""), types.EventKind(""), time.Time{}, time.Time{}, 10, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("len(filtered) = %d, want 1", len(filtered))
	}
	if diff := cmp.Diff("event-non-envelope", filtered[0].EventID().String()); diff != "" {
		t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
	}
}

func searchEventsMigrations() fstest.MapFS {
	return fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
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
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    input_original_bytes INTEGER NOT NULL DEFAULT 0,
    output_original_bytes INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER,
    failed INTEGER NOT NULL DEFAULT 0
);`),
		},
	}
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

	eventID, err := types.EventIDFrom(eventIDValue)
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	return model.EventOf(
		eventID,
		kind,
		types.Client("cli"),
		agent,
		sessionID,
		types.Workspace(workspace),
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
