package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_GetContextEvents(t *testing.T) {
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
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut := sqlite.NewDatasource(dbPath, migrations)
	if err := sut.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	firstEvent := newSearchEventFixture(
		t,
		"event-1",
		types.EventKindNote,
		"github.com/duck8823/traceary",
		"hello traceary",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), firstEvent); err != nil {
		t.Fatalf("Save(first) error = %v", err)
	}

	secondEvent := newSearchEventFixture(
		t,
		"event-2",
		types.EventKindNote,
		"github.com/duck8823/traceary",
		"follow up",
		time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), secondEvent); err != nil {
		t.Fatalf("Save(second) error = %v", err)
	}

	thirdEvent := newSearchEventFixture(
		t,
		"event-3",
		types.EventKindNote,
		"github.com/duck8823/other",
		"other repo",
		time.Date(2026, 4, 7, 14, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), thirdEvent); err != nil {
		t.Fatalf("Save(third) error = %v", err)
	}

	got, err := sut.GetContextEvents(context.Background(), port.GetContextInput{
		Workspace:      " github.com/duck8823/traceary ",
		SessionID: "session-1",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetContextEvents() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(got))
	}
	if got[0].EventID().String() != "event-2" {
		t.Fatalf("first EventID() = %q, want %q", got[0].EventID(), "event-2")
	}
}
