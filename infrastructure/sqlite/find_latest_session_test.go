package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/domain/port"
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

	t.Run("returns latest session_started", func(t *testing.T) {
		got, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			port.FindLatestSessionInput{
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

	t.Run("session with end boundary as last event is selected as latest", func(t *testing.T) {
		laterStartEvent := newFindLatestSessionEventFixture(
			t,
			"event-6",
			types.EventKindSessionStarted,
			"session-overlapping",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 11, 13, 0, 0, 0, time.UTC),
		)
		if err := sut.Save(context.Background(), dbPath, laterStartEvent); err != nil {
			t.Fatalf("Save(later start) error = %v", err)
		}

		overlapEndEvent := newFindLatestSessionEventFixture(
			t,
			"event-7",
			types.EventKindSessionEnded,
			"session-finished",
			"github.com/duck8823/traceary",
			"session ended",
			time.Date(2026, 4, 11, 14, 0, 0, 0, time.UTC),
		)
		if err := sut.Save(context.Background(), dbPath, overlapEndEvent); err != nil {
			t.Fatalf("Save(overlap end) error = %v", err)
		}

		got, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			port.FindLatestSessionInput{
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
		got, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			port.FindLatestSessionInput{
				Client:     "cli",
				Agent:      "codex",
				Repo:       "github.com/duck8823/traceary",
				ActiveOnly: true,
			},
		)
		if err != nil {
			t.Fatalf("FindLatestSessionStartedEvent() error = %v", err)
		}
		if got.EventID().String() != "event-6" {
			t.Fatalf("EventID() = %q, want %q", got.EventID(), "event-6")
		}
	})

	t.Run("returns newest start when multiple starts exist for same session_id", func(t *testing.T) {
		repeatedStartEvent := newFindLatestSessionEventFixture(
			t,
			"event-8",
			types.EventKindSessionStarted,
			"session-overlapping",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 11, 15, 0, 0, 0, time.UTC),
		)
		if err := sut.Save(context.Background(), dbPath, repeatedStartEvent); err != nil {
			t.Fatalf("Save(repeated start) error = %v", err)
		}

		got, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			port.FindLatestSessionInput{
				Client: "cli",
				Agent:  "codex",
				Repo:   "github.com/duck8823/traceary",
			},
		)
		if err != nil {
			t.Fatalf("FindLatestSessionStartedEvent() error = %v", err)
		}
		if got.EventID().String() != "event-8" {
			t.Fatalf("EventID() = %q, want %q", got.EventID(), "event-8")
		}
	})

	t.Run("returns error when no matching session exists", func(t *testing.T) {
		_, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			port.FindLatestSessionInput{Agent: "claude"},
		)
		if err == nil {
			t.Fatalf("FindLatestSessionStartedEvent() error = nil, want error")
		}
		if !errors.Is(err, port.ErrSessionNotFound) {
			t.Fatalf("error = %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("returns error when no matching active session exists", func(t *testing.T) {
		_, err := sut.FindLatestSessionStartedEvent(
			context.Background(),
			dbPath,
			port.FindLatestSessionInput{
				Agent:      "claude",
				ActiveOnly: true,
			},
		)
		if err == nil {
			t.Fatalf("FindLatestSessionStartedEvent() error = nil, want error")
		}
		if !errors.Is(err, port.ErrActiveSessionNotFound) {
			t.Fatalf("error = %v, want ErrActiveSessionNotFound", err)
		}
	})
}

func TestDatasource_FindLatestSessionStartedEvent_ignoresBoundariesFromOtherContexts(t *testing.T) {
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

	sharedStart := newFindLatestSessionEventFixture(
		t,
		"event-1",
		types.EventKindSessionStarted,
		"default",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, sharedStart); err != nil {
		t.Fatalf("Save(shared start) error = %v", err)
	}

	localLatest := newFindLatestSessionEventFixture(
		t,
		"event-2",
		types.EventKindSessionStarted,
		"session-local",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 9, 11, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, localLatest); err != nil {
		t.Fatalf("Save(local latest) error = %v", err)
	}

	otherRepoBoundary := newFindLatestSessionEventFixture(
		t,
		"event-3",
		types.EventKindSessionEnded,
		"default",
		"github.com/duck8823/other",
		"session ended",
		time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, otherRepoBoundary); err != nil {
		t.Fatalf("Save(other repo boundary) error = %v", err)
	}

	got, err := sut.FindLatestSessionStartedEvent(
		context.Background(),
		dbPath,
		port.FindLatestSessionInput{
			Client: "cli",
			Agent:  "codex",
			Repo:   "github.com/duck8823/traceary",
		},
	)
	if err != nil {
		t.Fatalf("FindLatestSessionStartedEvent() error = %v", err)
	}
	if got.EventID().String() != "event-2" {
		t.Fatalf("EventID() = %q, want %q", got.EventID(), "event-2")
	}
}

func TestDatasource_FindLatestSessionStartedEvent_activeOnlyIgnoresEndsFromOtherContexts(t *testing.T) {
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

	sharedStart := newFindLatestSessionEventFixture(
		t,
		"event-1",
		types.EventKindSessionStarted,
		"default",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, sharedStart); err != nil {
		t.Fatalf("Save(shared start) error = %v", err)
	}

	otherRepoEnd := newFindLatestSessionEventFixture(
		t,
		"event-2",
		types.EventKindSessionEnded,
		"default",
		"github.com/duck8823/other",
		"session ended",
		time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(context.Background(), dbPath, otherRepoEnd); err != nil {
		t.Fatalf("Save(other repo end) error = %v", err)
	}

	got, err := sut.FindLatestSessionStartedEvent(
		context.Background(),
		dbPath,
		port.FindLatestSessionInput{
			Client:     "cli",
			Agent:      "codex",
			Repo:       "github.com/duck8823/traceary",
			ActiveOnly: true,
		},
	)
	if err != nil {
		t.Fatalf("FindLatestSessionStartedEvent() error = %v", err)
	}
	if got.EventID().String() != "event-1" {
		t.Fatalf("EventID() = %q, want %q", got.EventID(), "event-1")
	}
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
