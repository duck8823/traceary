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
		t.Parallel()

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
		t.Parallel()

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
			types.EventKindSessionStarted,
			"session-ended-last",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 11, 12, 30, 0, 0, time.UTC),
		)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-8",
			types.EventKindSessionEnded,
			"session-ended-last",
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
		if diff := cmp.Diff("event-7", event.EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns active session when active only is set", func(t *testing.T) {
		t.Parallel()

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
			types.EventKindSessionStarted,
			"session-ended-later",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 11, 12, 30, 0, 0, time.UTC),
		)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-8",
			types.EventKindSessionEnded,
			"session-ended-later",
			"github.com/duck8823/traceary",
			"session ended",
			time.Date(2026, 4, 11, 14, 0, 0, 0, time.UTC),
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

	t.Run("active only keeps a session with events after its end marker", func(t *testing.T) {
		t.Parallel()

		eventDS, sessionDS := newFindLatestScenario(t)
		// session-late-events: started -> ended -> command_executed after the end.
		// The trailing command keeps the session active under activeOnly, so MCP
		// session_status agrees with the CLI snapshot's ended_with_late_events
		// rule instead of treating the lone session_ended as terminal (#1172).
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-late-start",
			types.EventKindSessionStarted,
			"session-late-events",
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-late-end",
			types.EventKindSessionEnded,
			"session-late-events",
			"github.com/duck8823/traceary",
			"session ended",
			time.Date(2026, 4, 12, 10, 30, 0, 0, time.UTC),
		)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-late-cmd",
			types.EventKindCommandExecuted,
			"session-late-events",
			"github.com/duck8823/traceary",
			"git status",
			time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC),
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
		if diff := cmp.Diff("event-late-start", event.EventID().String()); diff != "" {
			t.Fatalf("EventID() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns newest start when multiple starts exist for same session_id", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

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
		t.Parallel()

		eventDS, sessionDS := newFindLatestScenario(t)
		saveFindLatestSessionEventFixture(
			t,
			eventDS,
			"event-6",
			types.EventKindSessionEnded,
			"session-active",
			"github.com/duck8823/traceary",
			"session ended",
			time.Date(2026, 4, 11, 13, 0, 0, 0, time.UTC),
		)

		result, err := sessionDS.FindLatest(
			context.Background(),
			types.Client("cli"), types.Agent("codex"), types.Workspace("github.com/duck8823/traceary"), true,
		)
		if err != nil {
			t.Fatalf("FindLatest() error = %v, want nil", err)
		}
		if _, ok := result.Value(); ok {
			t.Fatalf("FindLatest() returned present, want empty")
		}
	})

	t.Run("returns empty Optional when active-only context has no matching session", func(t *testing.T) {
		t.Parallel()

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

	t.Run("handles repeated end boundary for already ended session", func(t *testing.T) {
		t.Parallel()

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
			"session ended again",
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

// TestDatasource_FindLatest_activeOnlyHonorsSessionsRowEndedAt asserts the
// active query consults sessions.ended_at, not just session_ended events, so a
// session closed by stale GC (which writes ended_at directly without an event)
// is excluded — matching the CLI snapshot. A GC-closed session that received
// later events stays active. This is the CLI/MCP agreement gap from #1172.
func TestDatasource_FindLatest_activeOnlyHonorsSessionsRowEndedAt(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, onDiskSQLiteMigrations(t))
	ctx := context.Background()
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const workspace = "github.com/duck8823/traceary"
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	saveRow := func(sessionID string, startedAt time.Time, endedAt time.Time) {
		sid, idErr := types.SessionIDFrom(sessionID)
		if idErr != nil {
			t.Fatalf("SessionIDFrom() error = %v", idErr)
		}
		session := model.SessionOf(sid, startedAt, types.Some(endedAt), types.Client("cli"), agent, types.Workspace(workspace), "", "", types.SessionID(""))
		if saveErr := sessionDS.SaveSessionBoundaryForTest(ctx, session); saveErr != nil {
			t.Fatalf("SaveSessionBoundaryForTest(%s) error = %v", sessionID, saveErr)
		}
	}

	// session-gc-closed starts latest, so if the active query ignored the row's
	// ended_at it would win the ORDER BY. Its ended_at is set with no later
	// events, so it must be excluded.
	saveFindLatestSessionEventFixture(t, eventDS, "gc-closed-start", types.EventKindSessionStarted, "session-gc-closed", workspace, "session started", time.Date(2026, 4, 12, 13, 0, 0, 0, time.UTC))
	saveRow("session-gc-closed", time.Date(2026, 4, 12, 13, 0, 0, 0, time.UTC), time.Date(2026, 4, 14, 13, 0, 0, 0, time.UTC))

	// session-gc-late has ended_at set too, but a later command arrived after
	// it, so it stays active (ended_with_late_events parity).
	saveFindLatestSessionEventFixture(t, eventDS, "gc-late-start", types.EventKindSessionStarted, "session-gc-late", workspace, "session started", time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC))
	saveRow("session-gc-late", time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 11, 30, 0, 0, time.UTC))
	saveFindLatestSessionEventFixture(t, eventDS, "gc-late-cmd", types.EventKindCommandExecuted, "session-gc-late", workspace, "git status", time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC))

	result, err := sessionDS.FindLatest(ctx, types.Client("cli"), types.Agent("codex"), types.Workspace(workspace), true)
	if err != nil {
		t.Fatalf("FindLatest() error = %v", err)
	}
	event, ok := result.Value()
	if !ok {
		t.Fatalf("FindLatest(activeOnly) returned empty, want the GC-closed-with-late-events session")
	}
	if diff := cmp.Diff("gc-late-start", event.EventID().String()); diff != "" {
		t.Fatalf("EventID() mismatch (-want +got):\n%s\n(a GC-closed session with no later events must be excluded)", diff)
	}
}

// TestDatasource_FindLatest_activeOnlyKeepsCrossAgentLateEvent asserts the
// late-event check is session_id-only, matching list_sessions.sql. A session
// ended by one agent that receives a later event under the same session_id but
// a different agent must stay active on both the CLI snapshot and the active
// query (#1172 CLI/MCP agreement).
func TestDatasource_FindLatest_activeOnlyKeepsCrossAgentLateEvent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, onDiskSQLiteMigrations(t))
	ctx := context.Background()
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const workspace = "github.com/duck8823/traceary"
	saveFindLatestSessionEventFixture(t, eventDS, "x-start", types.EventKindSessionStarted, "session-x", workspace, "session started", time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC))
	saveFindLatestSessionEventFixture(t, eventDS, "x-end", types.EventKindSessionEnded, "session-x", workspace, "session ended", time.Date(2026, 4, 12, 10, 30, 0, 0, time.UTC))

	claudeAgent, err := types.AgentFrom("claude")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	lateID, err := types.EventIDFrom("x-late-claude")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	lateEvent := model.EventOf(lateID, types.EventKindCommandExecuted, types.Client("cli"), claudeAgent, types.SessionID("session-x"), types.Workspace(workspace), "git status", time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC))
	if err := eventDS.Save(ctx, lateEvent); err != nil {
		t.Fatalf("Save(late claude event) error = %v", err)
	}

	result, err := sessionDS.FindLatest(ctx, types.Client("cli"), types.Agent("codex"), types.Workspace(workspace), true)
	if err != nil {
		t.Fatalf("FindLatest() error = %v", err)
	}
	event, ok := result.Value()
	if !ok {
		t.Fatalf("FindLatest(activeOnly) returned empty; a same-session late event from another agent must keep the session active")
	}
	if diff := cmp.Diff("x-start", event.EventID().String()); diff != "" {
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
