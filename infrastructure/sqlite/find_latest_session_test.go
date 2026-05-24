package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_FindLatest(t *testing.T) {
	t.Parallel()

	t.Run("returns latest session_started", func(t *testing.T) {
		_, sessionDS := newFindLatestScenario(t)

		result, err := sessionDS.FindLatest(
			context.Background(),
			types.Client("cli"), types.Agent("codex"), types.Workspace("github.com/duck8823/traceary"), false,
		)
		if err != nil {
			t.Fatalf("FindLatest() error = %v", err)
		}
		if _, ok := result.Value(); !ok {
			t.Fatalf("FindLatest() returned empty, want present")
		}
		event, _ := result.Value()
		if diff := cmp.Diff("event-4", event.EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("session with end boundary as last event is selected as latest", func(t *testing.T) {
		eventDS, sessionDS := newFindLatestScenario(t)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-6",
			types.EventKindSessionStarted,
			"session-overlapping",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 11, 13, 0, 0, 0, time.UTC),
		)

		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-7",
			types.EventKindSessionEnded,
			"session-finished",
			"github.com/duck8823/traceary",
			"session ended",
			time.Date(2026, 4, 11, 14, 0, 0, 0, time.UTC),
		)

		result, err := sessionDS.FindLatest(
			context.Background(),
			types.Client("cli"), types.Agent("codex"), types.Workspace("github.com/duck8823/traceary"), false,
		)
		if err != nil {
			t.Fatalf("FindLatest() error = %v", err)
		}
		if _, ok := result.Value(); !ok {
			t.Fatalf("FindLatest() returned empty, want present")
		}
		event, _ := result.Value()
		if diff := cmp.Diff("event-4", event.EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns active session when active only is set", func(t *testing.T) {
		eventDS, sessionDS := newFindLatestScenario(t)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-6",
			types.EventKindSessionStarted,
			"session-overlapping",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 11, 13, 0, 0, 0, time.UTC),
		)

		result, err := sessionDS.FindLatest(
			context.Background(),
			types.Client("cli"), types.Agent("codex"), types.Workspace("github.com/duck8823/traceary"), true,
		)
		if err != nil {
			t.Fatalf("FindLatest() error = %v", err)
		}
		if _, ok := result.Value(); !ok {
			t.Fatalf("FindLatest() returned empty, want present")
		}
		event, _ := result.Value()
		if diff := cmp.Diff("event-6", event.EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns newest start when multiple starts exist for same session_id", func(t *testing.T) {
		eventDS, sessionDS := newFindLatestScenario(t)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-6",
			types.EventKindSessionStarted,
			"session-overlapping",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 11, 13, 0, 0, 0, time.UTC),
		)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-8",
			types.EventKindSessionStarted,
			"session-overlapping",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 11, 15, 0, 0, 0, time.UTC),
		)

		result, err := sessionDS.FindLatest(
			context.Background(),
			types.Client("cli"), types.Agent("codex"), types.Workspace("github.com/duck8823/traceary"), false,
		)
		if err != nil {
			t.Fatalf("FindLatest() error = %v", err)
		}
		if _, ok := result.Value(); !ok {
			t.Fatalf("FindLatest() returned empty, want present")
		}
		event, _ := result.Value()
		if diff := cmp.Diff("event-8", event.EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns empty Optional when no matching session exists", func(t *testing.T) {
		_, sessionDS := newFindLatestScenario(t)

		result, err := sessionDS.FindLatest(
			context.Background(),
			types.Client(""), types.Agent("claude"), types.Workspace(""), false,
		)
		if err != nil {
			t.Fatalf("FindLatest() error = %v, want nil", err)
		}
		if _, ok := result.Value(); ok {
			t.Fatalf("FindLatest() returned present, want empty")
		}
	})

	t.Run("returns empty Optional when no matching active session exists", func(t *testing.T) {
		_, sessionDS := newFindLatestScenario(t)

		result, err := sessionDS.FindLatest(
			context.Background(),
			types.Client(""), types.Agent("claude"), types.Workspace(""), true,
		)
		if err != nil {
			t.Fatalf("FindLatest() error = %v, want nil", err)
		}
		if _, ok := result.Value(); ok {
			t.Fatalf("FindLatest() returned present, want empty")
		}
	})
}

func TestDatasource_FindLatest_ignoresBoundariesFromOtherContexts(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
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
	if err := eventDS.Save(context.Background(), sharedStart); err != nil {
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
	if err := eventDS.Save(context.Background(), localLatest); err != nil {
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
	if err := eventDS.Save(context.Background(), otherRepoBoundary); err != nil {
		t.Fatalf("Save(other workspace boundary) error = %v", err)
	}

	result, err := sessionDS.FindLatest(
		context.Background(),
		types.Client("cli"), types.Agent("codex"), types.Workspace("github.com/duck8823/traceary"), false,
	)
	if err != nil {
		t.Fatalf("FindLatest() error = %v", err)
	}
	if _, ok := result.Value(); !ok {
		t.Fatalf("FindLatest() returned empty, want present")
	}
	event, _ := result.Value()
	if diff := cmp.Diff("event-2", event.EventID().String()); diff != "" {
		t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_FindLatest_activeOnlyIgnoresEndsFromOtherContexts(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
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
	if err := eventDS.Save(context.Background(), sharedStart); err != nil {
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
	if err := eventDS.Save(context.Background(), otherRepoEnd); err != nil {
		t.Fatalf("Save(other workspace end) error = %v", err)
	}

	result, err := sessionDS.FindLatest(
		context.Background(),
		types.Client("cli"), types.Agent("codex"), types.Workspace("github.com/duck8823/traceary"), true,
	)
	if err != nil {
		t.Fatalf("FindLatest() error = %v", err)
	}
	if _, ok := result.Value(); !ok {
		t.Fatalf("FindLatest() returned empty, want present")
	}
	event, _ := result.Value()
	if diff := cmp.Diff("event-1", event.EventID().String()); diff != "" {
		t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
	}
}

func newFindLatestScenario(t *testing.T) (*sqlite.EventDatasource, *sqlite.SessionDatasource) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	saveFindLatestSessionEventFixture(
		t,
		eventDS,
		"event-1",
		types.EventKindSessionStarted,
		"session-old",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)
	saveFindLatestSessionEventFixture(
		t,
		eventDS,
		"event-2",
		types.EventKindSessionEnded,
		"session-old",
		"github.com/duck8823/traceary",
		"session ended",
		time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
	)
	saveFindLatestSessionEventFixture(
		t,
		eventDS,
		"event-3",
		types.EventKindSessionStarted,
		"session-active",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
	)
	saveFindLatestSessionEventFixture(
		t,
		eventDS,
		"event-4",
		types.EventKindSessionStarted,
		"session-finished",
		"github.com/duck8823/traceary",
		"session started",
		time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	)
	saveFindLatestSessionEventFixture(
		t,
		eventDS,
		"event-5",
		types.EventKindSessionEnded,
		"session-finished",
		"github.com/duck8823/traceary",
		"session ended",
		time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC),
	)

	return eventDS, sessionDS
}

func saveFindLatestSessionEventFixture(
	t *testing.T,
	eventDS *sqlite.EventDatasource,
	eventIDValue string,
	kind types.EventKind,
	sessionIDValue string,
	workspace string,
	body string,
	createdAt time.Time,
) {
	t.Helper()

	event := newFindLatestSessionEventFixture(t, eventIDValue, kind, sessionIDValue, workspace, body, createdAt)
	if err := eventDS.Save(context.Background(), event); err != nil {
		t.Fatalf("Save(%s) error = %v", eventIDValue, err)
	}
}

func newFindLatestSessionEventFixture(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
	sessionIDValue string,
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
	sessionID, err := types.SessionIDFrom(sessionIDValue)
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
