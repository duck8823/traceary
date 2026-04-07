package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
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

	oldEvent := newFindLatestSessionEventFixture(
		t,
		"event-1",
		types.EventKindSessionStarted,
		"session-old",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, oldEvent); err != nil {
		t.Fatalf("Save(old) error = %v", err)
	}

	endedEvent := newFindLatestSessionEventFixture(
		t,
		"event-2",
		types.EventKindSessionEnded,
		"session-old",
		"github.com/duck8823/traceary",
		"session ended",
		time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, endedEvent); err != nil {
		t.Fatalf("Save(ended) error = %v", err)
	}

	activeEvent := newFindLatestSessionEventFixture(
		t,
		"event-3",
		types.EventKindSessionStarted,
		"session-active",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, activeEvent); err != nil {
		t.Fatalf("Save(active) error = %v", err)
	}

	finishedStartEvent := newFindLatestSessionEventFixture(
		t,
		"event-4",
		types.EventKindSessionStarted,
		"session-finished",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, finishedStartEvent); err != nil {
		t.Fatalf("Save(finished start) error = %v", err)
	}

	finishedEndEvent := newFindLatestSessionEventFixture(
		t,
		"event-5",
		types.EventKindSessionEnded,
		"session-finished",
		"github.com/duck8823/traceary",
		"session ended",
		time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, finishedEndEvent); err != nil {
		t.Fatalf("Save(finished end) error = %v", err)
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
		if got.EventID().String() != "event-4" {
			t.Fatalf("EventID() = %q, want %q", got.EventID(), "event-4")
		}
	})

	t.Run("active only のとき未終了 session を返す", func(t *testing.T) {
		t.Parallel()

		got, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			queryservice.FindLatestSessionInput{
				Client:     "cli",
				Agent:      "codex",
				Repo:       "github.com/duck8823/traceary",
				ActiveOnly: true,
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
		if !errors.Is(err, queryservice.ErrSessionNotFound) {
			t.Fatalf("error = %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("一致する active session がなければエラー", func(t *testing.T) {
		t.Parallel()

		_, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			queryservice.FindLatestSessionInput{
				Agent:      "claude",
				ActiveOnly: true,
			},
		)
		if err == nil {
			t.Fatalf("FindLatestSessionStartedEvent() error = nil, want error")
		}
		if !errors.Is(err, queryservice.ErrActiveSessionNotFound) {
			t.Fatalf("error = %v, want ErrActiveSessionNotFound", err)
		}
	})
}

func newFindLatestSessionEventFixture(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
	sessionIDValue string,
	repo string,
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
	sessionID, err := types.SessionIDOf(sessionIDValue)
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	return model.EventOf(
		eventID,
		kind,
		"cli",
		agent,
		sessionID,
		repo,
		body,
		createdAt,
	)
}
