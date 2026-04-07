package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_FindLatestSessionStartedEvent(t *testing.T) {
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
ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut := sqlite.NewDatasource(migrations)
	if err := sut.Initialize(context.Background(), dbPath); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	oldEvent := newSearchEventFixture(
		t,
		"event-1",
		types.EventKindSessionStarted,
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, oldEvent); err != nil {
		t.Fatalf("Save(old) error = %v", err)
	}

	endedEvent := newSearchEventFixture(
		t,
		"event-2",
		types.EventKindSessionEnded,
		"github.com/duck8823/traceary",
		"session ended",
		time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, endedEvent); err != nil {
		t.Fatalf("Save(ended) error = %v", err)
	}

	latestEvent := newSearchEventFixture(
		t,
		"event-3",
		types.EventKindSessionStarted,
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, latestEvent); err != nil {
		t.Fatalf("Save(latest) error = %v", err)
	}

	t.Run("直近の session_started を返す", func(t *testing.T) {
		t.Parallel()

		got, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			queryservice.FindLatestSessionInput{
				Client: "cli",
				Agent:  "codex",
				Repo:   "github.com/duck8823/traceary",
			},
		)
		if err != nil {
			t.Fatalf("FindLatestSessionStartedEvent() error = %v", err)
		}
		if got.EventID().String() != "event-3" {
			t.Fatalf("EventID() = %q, want %q", got.EventID(), "event-3")
		}
	})

	t.Run("一致する session がなければエラー", func(t *testing.T) {
		t.Parallel()

		_, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			queryservice.FindLatestSessionInput{Agent: "claude"},
		)
		if err == nil {
			t.Fatalf("FindLatestSessionStartedEvent() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "条件に一致する session は存在しません") {
			t.Fatalf("error = %q, want no rows message", err.Error())
		}
	})
}
