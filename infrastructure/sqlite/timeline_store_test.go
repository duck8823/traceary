package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

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

	newDatasource := func(t *testing.T) *sqlite.Datasource {
		t.Helper()
		dbPath := filepath.Join(t.TempDir(), "timeline_test.db")
		ds := sqlite.NewDatasource(dbPath, migrations)
		if err := ds.Initialize(context.Background()); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}
		return ds
	}

	saveEvent := func(t *testing.T, ds *sqlite.Datasource, id string, workspace string, createdAt time.Time) {
		t.Helper()
		eventID, _ := types.EventIDOf(id)
		agent, _ := types.AgentOf("claude")
		sessionID, _ := types.SessionIDOf("session-1")
		event := model.EventOf(eventID, types.EventKindCommandExecuted, "hook", agent, sessionID, workspace, "cmd", createdAt)
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
		if blocks[0].EventCount != 2 {
			t.Fatalf("blocks[0].EventCount = %d, want 2", blocks[0].EventCount)
		}
		if blocks[1].EventCount != 3 {
			t.Fatalf("blocks[1].EventCount = %d, want 3", blocks[1].EventCount)
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
		if blocks[0].EventCount != 2 {
			t.Fatalf("blocks[0].EventCount = %d, want 2 (ws-a only)", blocks[0].EventCount)
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
