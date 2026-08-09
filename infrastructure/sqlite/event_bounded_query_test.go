package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// TestEventBoundedQuery_HydratesEmptyCommandExecutedBodies pins the default
// MCP bounded path: after #1675 the envelope body is empty for audited
// commands, so hydrateBoundedEvents must decode command_text and apply the
// same rune limit / visible_body_runes contract as the SQL path.
func TestEventBoundedQuery_HydratesEmptyCommandExecutedBodies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const (
		sessionID    = "session-bounded-cmd"
		commandLine  = "go test ./infrastructure/sqlite -run TestEventBounded -count=1"
		bodyRuneLimit = 12
	)
	eventID := mustEventIDForSQLite(t, "event-bounded-cmd")
	event := model.EventOf(
		eventID,
		types.EventKindCommandExecuted,
		types.Client("hook"),
		mustAgentForSQLite(t, "codex"),
		mustSessionIDForSQLite(t, sessionID),
		types.Workspace("repo-cmd"),
		"",
		time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
	)
	audit, err := model.NewCommandAudit(eventID, commandLine, "stdin", "stdout", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}
	if err := sut.SaveWithAudit(ctx, event, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	got, err := sut.ListRecentBounded(
		ctx,
		apptypes.NewEventListCriteriaBuilder(1).
			SessionID(types.SessionID(sessionID)).
			Kind(types.EventKindCommandExecuted).
			Build(),
		bodyRuneLimit,
	)
	if err != nil {
		t.Fatalf("ListRecentBounded() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListRecentBounded() len = %d, want 1", len(got))
	}

	wantBody := string([]rune(commandLine)[:bodyRuneLimit])
	if got[0].Body() != wantBody {
		t.Fatalf("bounded command body = %q, want %q", got[0].Body(), wantBody)
	}
	if got[0].VisibleBodyRunes() != len([]rune(commandLine)) {
		t.Fatalf("VisibleBodyRunes() = %d, want %d", got[0].VisibleBodyRunes(), len([]rune(commandLine)))
	}
	if !got[0].BodyResponseTruncated() {
		t.Fatalf("BodyResponseTruncated() = false, want true for limited command line")
	}
}

func TestEventBoundedQuery_ReturnsSmallPrefixFromLargeStoredBody(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(
		t,
		dbPath,
		migrationsInRangeExcluding(t, onDiskSQLiteMigrationDir(t), 1, 34, 32),
	)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const storedRunes = 8 << 20
	body := strings.Repeat("x", storedRunes)
	event := model.EventOf(
		mustEventIDForSQLite(t, "event-large-bounded"),
		types.EventKindNote,
		types.Client("hook"),
		mustAgentForSQLite(t, "codex"),
		mustSessionIDForSQLite(t, "session-large"),
		types.Workspace("repo-large"),
		body,
		time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(ctx, event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE events
		   SET body_original_bytes = ?,
		       body_ingest_truncated = 1,
		       body_storage_truncated = 0
		 WHERE id = ?`,
		storedRunes+4096,
		event.EventID().String(),
	); err != nil {
		_ = db.Close()
		t.Fatalf("update body extent: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close extent DB: %v", err)
	}

	const bodyLimit = 128
	got, err := sut.ListRecentBounded(
		ctx,
		apptypes.NewEventListCriteriaBuilder(1).
			SessionID(types.SessionID("session-large")).
			Build(),
		bodyLimit,
	)
	if err != nil {
		t.Fatalf("ListRecentBounded() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListRecentBounded() len = %d, want 1", len(got))
	}
	if bodyRunes := len([]rune(got[0].Body())); bodyRunes != bodyLimit {
		t.Fatalf("bounded body runes = %d, want %d", bodyRunes, bodyLimit)
	}
	if got[0].VisibleBodyRunes() != storedRunes || !got[0].BodyResponseTruncated() {
		t.Fatalf(
			"visible body = (runes=%d, truncated=%t), want (%d, true)",
			got[0].VisibleBodyRunes(),
			got[0].BodyResponseTruncated(),
			storedRunes,
		)
	}
	extent := got[0].Metadata().BodyExtent()
	if extent.StoredBytes() != len(body) {
		t.Fatalf("stored bytes = %d, want %d", extent.StoredBytes(), len(body))
	}
	originalBytes, ok := extent.OriginalBytes().Value()
	if !ok || originalBytes != storedRunes+4096 {
		t.Fatalf("original bytes = (%d, %t), want (%d, true)", originalBytes, ok, storedRunes+4096)
	}
	ingest, ok := extent.IngestTruncated().Value()
	if !ok || !ingest {
		t.Fatalf("ingest truncation = (%t, %t), want (true, true)", ingest, ok)
	}
}

func TestEventBoundedQuery_MatchesCanonicalVisibleTextProjection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(
		t,
		dbPath,
		migrationsInRangeExcluding(t, onDiskSQLiteMigrationDir(t), 1, 34, 32),
	)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	orderedBlocks := make([]apptypes.EventBodyBlock, 12)
	for index := range orderedBlocks {
		orderedBlocks[index] = apptypes.EventBodyBlock{
			Type: apptypes.EventBodyBlockTypeText,
			Text: "block-" + string(rune('a'+index)),
		}
	}
	orderedEnvelope, err := apptypes.MarshalEventBodyBlocks(orderedBlocks)
	if err != nil {
		t.Fatalf("MarshalEventBodyBlocks() error = %v", err)
	}
	tests := []struct {
		name string
		body string
	}{
		{
			name: "canonical excludes thinking and joins text",
			body: `{"blocks":[{"type":"thinking","text":"hidden"},{"type":"text","text":"first"},{"type":"text","text":"second"}]}`,
		},
		{
			name: "canonical unknown block stays excluded",
			body: `{"blocks":[{"type":"tool_use","text":"","id":"call-1"},{"type":"text","text":"visible"}]}`,
		},
		{
			name: "canonical skips Unicode whitespace-only text",
			body: `{"blocks":[{"type":"text","text":"\u00a0\u2003"},{"type":"text","text":"visible"}]}`,
		},
		{
			name: "canonical preserves nonblank block whitespace",
			body: `{"blocks":[{"type":"text","text":"  visible  "}]}`,
		},
		{
			name: "canonical preserves numeric array order beyond ten blocks",
			body: orderedEnvelope,
		},
		{name: "canonical empty envelope", body: `{"blocks":[]}`},
		{name: "foreign blocks JSON stays raw", body: `{"blocks":[{"foo":"bar"}]}`},
		{name: "capitalized Blocks stays raw", body: `{"Blocks":[{"type":"text","text":"visible"}]}`},
		{name: "malformed JSON stays raw", body: `{not json`},
		{name: "legacy plain text stays raw", body: "legacy visible body"},
	}
	base := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	for index, tt := range tests {
		eventID := "event-visible-" + strings.ReplaceAll(tt.name, " ", "-")
		sessionID := "session-visible-" + string(rune('a'+index))
		event := model.EventOf(
			mustEventIDForSQLite(t, eventID),
			types.EventKindTranscript,
			types.Client("hook"),
			mustAgentForSQLite(t, "codex"),
			mustSessionIDForSQLite(t, sessionID),
			types.Workspace("repo-visible"),
			tt.body,
			base.Add(time.Duration(index)*time.Second),
		)
		if err := sut.Save(ctx, event); err != nil {
			t.Fatalf("Save(%s) error = %v", tt.name, err)
		}
		got, err := sut.ListRecentBounded(
			ctx,
			apptypes.NewEventListCriteriaBuilder(1).SessionID(types.SessionID(sessionID)).Build(),
			4096,
		)
		if err != nil {
			t.Fatalf("ListRecentBounded(%s) error = %v", tt.name, err)
		}
		if len(got) != 1 {
			t.Fatalf("ListRecentBounded(%s) len = %d, want 1", tt.name, len(got))
		}
		want := apptypes.ExtractPlainBody(tt.body)
		if got[0].Body() != want {
			t.Fatalf("visible body (%s) = %q, want %q", tt.name, got[0].Body(), want)
		}
		_, wantCanonical := apptypes.DecodeCanonicalEnvelope(tt.body)
		if got[0].CanonicalEnvelope() != wantCanonical {
			t.Fatalf(
				"CanonicalEnvelope(%s) = %t, want %t",
				tt.name,
				got[0].CanonicalEnvelope(),
				wantCanonical,
			)
		}
	}
}

func TestEventBoundedQuery_PreservesRetentionUnavailability(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(
		t,
		dbPath,
		migrationsInRangeExcluding(t, onDiskSQLiteMigrationDir(t), 1, 34, 32),
	)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	const retainedCanonicalBody = `{"blocks":[{"type":"text","text":"must remain unavailable"}]}`
	event := newEventForSQLiteTest(
		t,
		"event-retention-bounded",
		"hook",
		"codex",
		"session-retention",
		"repo-retention",
		retainedCanonicalBody,
		time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(ctx, event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
			UPDATE events
			   SET body = ?, body_availability = ?
			 WHERE id = ?`,
		retainedCanonicalBody,
		types.BodyAvailabilityUnavailableRetention.String(),
		event.EventID().String(),
	); err != nil {
		_ = db.Close()
		t.Fatalf("mark body unavailable: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close retention DB: %v", err)
	}

	got, err := sut.GetContextBounded(
		ctx,
		apptypes.NewEventContextCriteriaBuilder(1).SessionID("session-retention").Build(),
		100,
	)
	if err != nil {
		t.Fatalf("GetContextBounded() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("GetContextBounded() len = %d, want 1", len(got))
	}
	if got[0].Body() != "" || got[0].VisibleBodyRunes() != 0 ||
		got[0].BodyAvailability() != types.BodyAvailabilityUnavailableRetention ||
		got[0].CanonicalEnvelope() {
		t.Fatalf("retention bounded event = %+v", got[0])
	}
}
