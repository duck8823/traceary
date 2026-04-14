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
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_ListTimelineBlocks(t *testing.T) {
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
	}

	newDatasource := func(t *testing.T) *sqlite.EventDatasource {
		t.Helper()
		dbPath := filepath.Join(t.TempDir(), "timeline_test.db")
		ds, storeManager := newEventDatasource(t, dbPath, migrations)
		if err := storeManager.Initialize(context.Background()); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}
		return ds
	}

	saveEvent := func(t *testing.T, ds *sqlite.EventDatasource, id string, workspace string, createdAt time.Time) {
		t.Helper()
		eventID, _ := types.EventIDOf(id)
		agent, _ := types.AgentOf("claude")
		sessionID, _ := types.SessionIDOf("session-1")
		event := model.EventOf(eventID, types.EventKindCommandExecuted, types.Client("hook"), agent, sessionID, types.Workspace(workspace), "cmd", createdAt)
		if err := ds.Save(context.Background(), event); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	t.Run("detects gap between events", func(t *testing.T) {
		t.Parallel()
		ds := newDatasource(t)

		// Block 1: 09:00, 09:05, 09:10
		saveEvent(t, ds, "e1", "ws", time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC))
		saveEvent(t, ds, "e2", "ws", time.Date(2026, 4, 10, 9, 5, 0, 0, time.UTC))
		saveEvent(t, ds, "e3", "ws", time.Date(2026, 4, 10, 9, 10, 0, 0, time.UTC))
		// Gap: 30 minutes
		// Block 2: 09:40, 09:45
		saveEvent(t, ds, "e4", "ws", time.Date(2026, 4, 10, 9, 40, 0, 0, time.UTC))
		saveEvent(t, ds, "e5", "ws", time.Date(2026, 4, 10, 9, 45, 0, 0, time.UTC))

		blocks, err := ds.ListTimelineBlocks(context.Background(), types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
		if err != nil {
			t.Fatalf("ListTimelineBlocks() error = %v", err)
		}
		if len(blocks) != 2 {
			t.Fatalf("len(blocks) = %d, want 2", len(blocks))
		}
		// Blocks are ordered DESC, so block 2 first
		if diff := cmp.Diff(2, blocks[0].EventCount()); diff != "" {
			t.Fatalf("blocks[0].EventCount() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(3, blocks[1].EventCount()); diff != "" {
			t.Fatalf("blocks[1].EventCount() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("filters by workspace", func(t *testing.T) {
		t.Parallel()
		ds := newDatasource(t)

		saveEvent(t, ds, "e1", "ws-a", time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC))
		saveEvent(t, ds, "e2", "ws-b", time.Date(2026, 4, 10, 9, 5, 0, 0, time.UTC))
		saveEvent(t, ds, "e3", "ws-a", time.Date(2026, 4, 10, 9, 10, 0, 0, time.UTC))

		blocks, err := ds.ListTimelineBlocks(context.Background(), types.Workspace("ws-a"), time.Time{}, time.Time{}, 900, 10)
		if err != nil {
			t.Fatalf("ListTimelineBlocks() error = %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("len(blocks) = %d, want 1", len(blocks))
		}
		if diff := cmp.Diff(2, blocks[0].EventCount()); diff != "" {
			t.Fatalf("blocks[0].EventCount() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns empty for no events", func(t *testing.T) {
		t.Parallel()
		ds := newDatasource(t)

		blocks, err := ds.ListTimelineBlocks(context.Background(), types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
		if err != nil {
			t.Fatalf("ListTimelineBlocks() error = %v", err)
		}
		if len(blocks) != 0 {
			t.Fatalf("len(blocks) = %d, want 0", len(blocks))
		}
	})

	saveEventKind := func(t *testing.T, ds *sqlite.EventDatasource, id string, workspace string, kind types.EventKind, body string, createdAt time.Time) {
		t.Helper()
		eventID, _ := types.EventIDOf(id)
		agent, _ := types.AgentOf("claude")
		sessionID, _ := types.SessionIDOf("session-1")
		event := model.EventOf(eventID, kind, types.Client("hook"), agent, sessionID, types.Workspace(workspace), body, createdAt)
		if err := ds.Save(context.Background(), event); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	t.Run("provides per-workspace breakdown with compact_summary fallback", func(t *testing.T) {
		t.Parallel()
		ds := newDatasource(t)

		// Single block spanning two workspaces.
		saveEvent(t, ds, "e1", "ws-a", time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC))
		saveEventKind(t, ds, "e2", "ws-a", types.EventKindPrompt, "user wanted feature X", time.Date(2026, 4, 10, 9, 1, 0, 0, time.UTC))
		saveEventKind(t, ds, "e3", "ws-a", types.EventKindCompactSummary, "shipped feature X via PR #1", time.Date(2026, 4, 10, 9, 8, 0, 0, time.UTC))
		saveEvent(t, ds, "e4", "ws-b", time.Date(2026, 4, 10, 9, 5, 0, 0, time.UTC))
		saveEventKind(t, ds, "e5", "ws-b", types.EventKindPrompt, "triage bug Y", time.Date(2026, 4, 10, 9, 6, 0, 0, time.UTC))

		blocks, err := ds.ListTimelineBlocks(context.Background(), types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
		if err != nil {
			t.Fatalf("ListTimelineBlocks() error = %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("len(blocks) = %d, want 1", len(blocks))
		}
		breakdown := blocks[0].WorkspaceBreakdown()
		byWs := make(map[string]int, len(breakdown))
		for i, ws := range breakdown {
			byWs[ws.Workspace()] = i
		}
		wsA, ok := byWs["ws-a"]
		if !ok {
			t.Fatalf("breakdown missing ws-a: %+v", breakdown)
		}
		if got := breakdown[wsA].Summary(); got != "shipped feature X via PR #1" {
			t.Errorf("ws-a summary = %q, want compact_summary body", got)
		}
		if got := breakdown[wsA].SummarySource(); got != "compact_summary" {
			t.Errorf("ws-a source = %q, want compact_summary", got)
		}
		wsB, ok := byWs["ws-b"]
		if !ok {
			t.Fatalf("breakdown missing ws-b: %+v", breakdown)
		}
		if got := breakdown[wsB].Summary(); got != "triage bug Y" {
			t.Errorf("ws-b summary = %q, want first prompt body", got)
		}
		if got := breakdown[wsB].SummarySource(); got != "prompt" {
			t.Errorf("ws-b source = %q, want prompt", got)
		}
	})

	t.Run("falls back to kind_counts when no summary candidate", func(t *testing.T) {
		t.Parallel()
		ds := newDatasource(t)

		saveEvent(t, ds, "e1", "ws-only-commands", time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC))
		saveEvent(t, ds, "e2", "ws-only-commands", time.Date(2026, 4, 10, 9, 1, 0, 0, time.UTC))

		blocks, err := ds.ListTimelineBlocks(context.Background(), types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
		if err != nil {
			t.Fatalf("ListTimelineBlocks() error = %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("len(blocks) = %d, want 1", len(blocks))
		}
		breakdown := blocks[0].WorkspaceBreakdown()
		if len(breakdown) != 1 {
			t.Fatalf("breakdown len = %d, want 1", len(breakdown))
		}
		if got := breakdown[0].Summary(); got != "" {
			t.Errorf("summary = %q, want empty string", got)
		}
		if got := breakdown[0].SummarySource(); got != "kind_counts" {
			t.Errorf("source = %q, want kind_counts", got)
		}
	})

	t.Run("filters empty workspace rows out of breakdown", func(t *testing.T) {
		t.Parallel()
		ds := newDatasource(t)

		// Mix a legacy empty-workspace row with a normal one in the same block.
		saveEvent(t, ds, "e1", "", time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC))
		saveEvent(t, ds, "e2", "ws-real", time.Date(2026, 4, 10, 9, 1, 0, 0, time.UTC))
		saveEvent(t, ds, "e3", "ws-real", time.Date(2026, 4, 10, 9, 2, 0, 0, time.UTC))

		blocks, err := ds.ListTimelineBlocks(context.Background(), types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
		if err != nil {
			t.Fatalf("ListTimelineBlocks() error = %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("len(blocks) = %d, want 1", len(blocks))
		}
		breakdown := blocks[0].WorkspaceBreakdown()
		if len(breakdown) != 1 {
			t.Fatalf("breakdown len = %d, want 1 (empty workspace must not leak)", len(breakdown))
		}
		if got := breakdown[0].Workspace(); got != "ws-real" {
			t.Errorf("breakdown[0].Workspace() = %q, want %q", got, "ws-real")
		}
		if got := blocks[0].EventCount(); got != 3 {
			t.Errorf("block event count = %d, want 3 (still counts empty-workspace event)", got)
		}
	})

	t.Run("blank summary body is skipped in favor of a later non-blank candidate", func(t *testing.T) {
		t.Parallel()
		ds := newDatasource(t)

		// First prompt is whitespace-only, second is real content.
		saveEventKind(t, ds, "e1", "ws-prompt", types.EventKindPrompt, "   ", time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC))
		saveEventKind(t, ds, "e2", "ws-prompt", types.EventKindPrompt, "fix the real thing", time.Date(2026, 4, 10, 10, 1, 0, 0, time.UTC))
		// Blank compact_summary must not override a real prompt fallback.
		saveEventKind(t, ds, "e3", "ws-compact", types.EventKindCompactSummary, "\t\n", time.Date(2026, 4, 10, 10, 2, 0, 0, time.UTC))
		saveEventKind(t, ds, "e4", "ws-compact", types.EventKindPrompt, "user intent", time.Date(2026, 4, 10, 10, 3, 0, 0, time.UTC))

		blocks, err := ds.ListTimelineBlocks(context.Background(), types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
		if err != nil {
			t.Fatalf("ListTimelineBlocks() error = %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("len(blocks) = %d, want 1", len(blocks))
		}
		byWs := make(map[string]int, len(blocks[0].WorkspaceBreakdown()))
		breakdown := blocks[0].WorkspaceBreakdown()
		for i, ws := range breakdown {
			byWs[ws.Workspace()] = i
		}
		promptIdx, ok := byWs["ws-prompt"]
		if !ok {
			t.Fatalf("breakdown missing ws-prompt: %+v", breakdown)
		}
		if got := breakdown[promptIdx].Summary(); got != "fix the real thing" {
			t.Errorf("ws-prompt summary = %q, want 'fix the real thing' (blank prompt must be skipped)", got)
		}
		compactIdx, ok := byWs["ws-compact"]
		if !ok {
			t.Fatalf("breakdown missing ws-compact: %+v", breakdown)
		}
		if got := breakdown[compactIdx].Summary(); got != "user intent" {
			t.Errorf("ws-compact summary = %q, want 'user intent' (blank compact_summary must fall through to prompt)", got)
		}
		if got := breakdown[compactIdx].SummarySource(); got != "prompt" {
			t.Errorf("ws-compact source = %q, want 'prompt'", got)
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		t.Parallel()
		ds := newDatasource(t)

		// 3 blocks with 30min gaps
		saveEvent(t, ds, "e1", "ws", time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC))
		saveEvent(t, ds, "e2", "ws", time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC))
		saveEvent(t, ds, "e3", "ws", time.Date(2026, 4, 10, 11, 0, 0, 0, time.UTC))

		blocks, err := ds.ListTimelineBlocks(context.Background(), types.Workspace(""), time.Time{}, time.Time{}, 900, 2)
		if err != nil {
			t.Fatalf("ListTimelineBlocks() error = %v", err)
		}
		if len(blocks) != 2 {
			t.Fatalf("len(blocks) = %d, want 2", len(blocks))
		}
	})
}
