package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

// saveDedupTestEvent persists an event through EventDatasource.Save, the path
// that carries the hook content-event duplicate-window guard.
func saveDedupTestEvent(
	ctx context.Context,
	t *testing.T,
	sut *sqlite.EventDatasource,
	id string,
	kind types.EventKind,
	client string,
	sourceHook string,
	body string,
	at time.Time,
) {
	t.Helper()

	eventID, err := types.EventIDFrom(id)
	if err != nil {
		t.Fatalf("EventIDFrom(%s) error = %v", id, err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	event := model.EventOfWithSourceHook(
		eventID,
		kind,
		types.Client(client),
		agent,
		sessionID,
		types.Workspace("github.com/duck8823/dotfiles"),
		body,
		at,
		sourceHook,
	)
	if err := sut.Save(ctx, event); err != nil {
		t.Fatalf("Save(%s) error = %v", id, err)
	}
}

func countEventsByKind(t *testing.T, dbPath string, kind types.EventKind) int {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ?`, kind.String()).Scan(&count); err != nil {
		t.Fatalf("count query error = %v", err)
	}
	return count
}

func eventRowExists(t *testing.T, dbPath, id string) bool {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("id count query error = %v", err)
	}
	return count > 0
}

// TestDatasource_Save_SkipsDuplicateHookContentEventsWithinWindow covers the
// prompt and transcript duplicate windows separately (#1167 criteria 1, 2, 4).
// For each kind: the original is kept, an identical body within the window is
// suppressed, a different body within the window is kept, and an identical body
// outside the window is kept.
func TestDatasource_Save_SkipsDuplicateHookContentEventsWithinWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		kind       types.EventKind
		sourceHook string
	}{
		{name: "prompt", kind: types.EventKindPrompt, sourceHook: "user_prompt_submit"},
		{name: "transcript", kind: types.EventKindTranscript, sourceHook: "stop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
			sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
			if err := storeManager.Initialize(ctx); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}

			base := time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC)
			saveDedupTestEvent(ctx, t, sut, "event-original", tt.kind, "hook", tt.sourceHook, "same body", base)
			saveDedupTestEvent(ctx, t, sut, "event-duplicate", tt.kind, "hook", tt.sourceHook, "same body", base.Add(time.Second))
			saveDedupTestEvent(ctx, t, sut, "event-different-body", tt.kind, "hook", tt.sourceHook, "different body", base.Add(time.Second))
			saveDedupTestEvent(ctx, t, sut, "event-later-rerun", tt.kind, "hook", tt.sourceHook, "same body", base.Add(10*time.Second))

			if diff := cmp.Diff(3, countEventsByKind(t, dbPath, tt.kind)); diff != "" {
				t.Fatalf("persisted %s event count mismatch (-want +got):\n%s", tt.name, diff)
			}
			if eventRowExists(t, dbPath, "event-duplicate") {
				t.Fatalf("duplicate %s within window was persisted, want suppressed", tt.name)
			}
			if !eventRowExists(t, dbPath, "event-different-body") {
				t.Fatalf("different-body %s within window was suppressed, want persisted", tt.name)
			}
			if !eventRowExists(t, dbPath, "event-later-rerun") {
				t.Fatalf("identical %s outside window was suppressed, want persisted", tt.name)
			}
		})
	}
}

// TestDatasource_Save_DoesNotDeduplicateNonHookContentEvents proves the guard is
// gated on hook origin: a direct CLI/MCP write (client != "hook") is never
// suppressed even with an identical body within the window.
func TestDatasource_Save_DoesNotDeduplicateNonHookContentEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC)
	saveDedupTestEvent(ctx, t, sut, "cli-original", types.EventKindPrompt, "cli", "", "same body", base)
	saveDedupTestEvent(ctx, t, sut, "cli-duplicate", types.EventKindPrompt, "cli", "", "same body", base.Add(time.Second))

	if diff := cmp.Diff(2, countEventsByKind(t, dbPath, types.EventKindPrompt)); diff != "" {
		t.Fatalf("non-hook prompt count mismatch (-want +got):\n%s", diff)
	}
}

// TestDatasource_Save_DoesNotDeduplicateNonContentHookEvents proves the guard is
// gated on kind: a hook-originated note (a non prompt/transcript kind) is never
// suppressed. This protects the non-goal — only prompt/transcript are eligible.
func TestDatasource_Save_DoesNotDeduplicateNonContentHookEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC)
	saveDedupTestEvent(ctx, t, sut, "note-original", types.EventKindNote, "hook", "", "same body", base)
	saveDedupTestEvent(ctx, t, sut, "note-duplicate", types.EventKindNote, "hook", "", "same body", base.Add(time.Second))

	if diff := cmp.Diff(2, countEventsByKind(t, dbPath, types.EventKindNote)); diff != "" {
		t.Fatalf("hook note count mismatch (-want +got):\n%s", diff)
	}
}

// TestDatasource_Save_DoesNotDeduplicateAcrossKinds proves a prompt never
// deduplicates a transcript: the duplicate query binds the event's own kind, so
// identical bodies of different kinds at the same instant both persist.
func TestDatasource_Save_DoesNotDeduplicateAcrossKinds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	at := time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC)
	saveDedupTestEvent(ctx, t, sut, "prompt-row", types.EventKindPrompt, "hook", "user_prompt_submit", "identical body", at)
	saveDedupTestEvent(ctx, t, sut, "transcript-row", types.EventKindTranscript, "hook", "stop", "identical body", at)

	if diff := cmp.Diff(1, countEventsByKind(t, dbPath, types.EventKindPrompt)); diff != "" {
		t.Fatalf("prompt count mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, countEventsByKind(t, dbPath, types.EventKindTranscript)); diff != "" {
		t.Fatalf("transcript count mismatch (-want +got):\n%s", diff)
	}
}

// lockedBuffer is a concurrency-safe io.Writer for capturing slog output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	//nolint:wrapcheck // io.Writer contract: forward the underlying writer's result verbatim.
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestDatasource_Save_LogsSuppressedDuplicateHookContentEvent verifies the
// suppression is observable via debug output (#1167 criterion 3). It swaps the
// global slog default, so it must run in the sequential (non-parallel) phase to
// avoid interleaving with other tests' debug logs.
func TestDatasource_Save_LogsSuppressedDuplicateHookContentEvent(t *testing.T) {
	buf := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC)
	saveDedupTestEvent(ctx, t, sut, "log-original", types.EventKindPrompt, "hook", "user_prompt_submit", "same body", base)
	saveDedupTestEvent(ctx, t, sut, "log-duplicate", types.EventKindPrompt, "hook", "user_prompt_submit", "same body", base.Add(time.Second))

	if !strings.Contains(buf.String(), "suppressed duplicate hook content event within window") {
		t.Fatalf("expected suppression debug log, got:\n%s", buf.String())
	}
}
