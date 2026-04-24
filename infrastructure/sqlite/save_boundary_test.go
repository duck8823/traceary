package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestSessionDatasource_SaveBoundary_Start(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := infra.NewSessionDatasource(db)
	eventDS := infra.NewEventDatasource(db)

	sessionID, _ := types.SessionIDOf("session-start")
	agent, _ := types.AgentOf("claude")
	startedAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	session := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))

	eventID, _ := types.EventIDOf("event-start")
	event := model.EventOf(
		eventID,
		types.EventKindSessionStarted,
		types.Client("cli"),
		agent,
		sessionID,
		types.Workspace("workspace"),
		"session started",
		startedAt,
	)

	if err := sessionDS.SaveBoundary(ctx, session, event); err != nil {
		t.Fatalf("SaveBoundary() error = %v", err)
	}

	// Session row was inserted
	stored, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if _, ok := stored.Value(); !ok {
		t.Fatalf("FindByID() should be present after SaveBoundary")
	}

	// Event row was inserted
	events, err := eventDS.ListRecent(ctx, 10, 0, types.EventKindSessionStarted, types.Client(""), types.Agent(""), sessionID, types.Workspace(""), false, time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ListRecent() len = %d, want 1", len(events))
	}
	if diff := cmp.Diff("event-start", events[0].EventID().String()); diff != "" {
		t.Errorf("EventID mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionDatasource_SaveBoundary_End(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := infra.NewSessionDatasource(db)

	sessionID, _ := types.SessionIDOf("session-end")
	agent, _ := types.AgentOf("claude")
	startedAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)

	// First, start the session.
	startSession := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
	startEventID, _ := types.EventIDOf("event-start")
	startEvent := model.EventOf(
		startEventID, types.EventKindSessionStarted,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session started", startedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, startSession, startEvent); err != nil {
		t.Fatalf("SaveBoundary(start) error = %v", err)
	}

	// Now end it.
	endedAt := startedAt.Add(time.Hour)
	storedOpt, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	storedSession, _ := storedOpt.Value()
	if err := storedSession.End(endedAt, "wrapped up"); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	endEventID, _ := types.EventIDOf("event-end")
	endEvent := model.EventOf(
		endEventID, types.EventKindSessionEnded,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session ended", endedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, storedSession, endEvent); err != nil {
		t.Fatalf("SaveBoundary(end) error = %v", err)
	}

	// Verify the session row was updated.
	updatedOpt, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID(after end) error = %v", err)
	}
	updated, _ := updatedOpt.Value()
	if _, ok := updated.EndedAt().Value(); !ok {
		t.Fatalf("EndedAt() should be present after end")
	}
	gotEndedAt, _ := updated.EndedAt().Value()
	if !gotEndedAt.Equal(endedAt) {
		t.Errorf("EndedAt() = %v, want %v", gotEndedAt, endedAt)
	}
	if diff := cmp.Diff("wrapped up", updated.Summary()); diff != "" {
		t.Errorf("Summary mismatch (-want +got):\n%s", diff)
	}
}

// TestSessionDatasource_Save_LabelDoesNotTouchEndedAt asserts that persisting
// a label change leaves the ended_at column untouched. Without this guarantee,
// a session label operation that interleaves with a session end would clobber
// the ended_at timestamp.
func TestSessionDatasource_Save_LabelDoesNotTouchEndedAt(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := infra.NewSessionDatasource(db)

	sessionID, _ := types.SessionIDOf("session-label-only")
	agent, _ := types.AgentOf("claude")
	startedAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)

	startSession := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
	startEventID, _ := types.EventIDOf("event-start")
	startEvent := model.EventOf(
		startEventID, types.EventKindSessionStarted,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session started", startedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, startSession, startEvent); err != nil {
		t.Fatalf("SaveBoundary(start) error = %v", err)
	}

	storedOpt, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	stored, _ := storedOpt.Value()
	stored.SetLabel("sprint-1")
	if err := sessionDS.Save(ctx, stored); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	after, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID(after label) error = %v", err)
	}
	got, _ := after.Value()
	if _, ok := got.EndedAt().Value(); ok {
		t.Errorf("EndedAt() should stay empty after label-only Save, got %v", got.EndedAt())
	}
	if diff := cmp.Diff("sprint-1", got.Label()); diff != "" {
		t.Errorf("Label mismatch (-want +got):\n%s", diff)
	}
}

// TestSessionDatasource_SaveBoundary_EndPreservesLabel asserts that ending a
// session does not erase a label applied concurrently. This is the lost
// update scenario Codex previously reproduced.
func TestSessionDatasource_SaveBoundary_EndPreservesLabel(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := infra.NewSessionDatasource(db)

	sessionID, _ := types.SessionIDOf("session-label-vs-end")
	agent, _ := types.AgentOf("claude")
	startedAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)

	startSession := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
	startEventID, _ := types.EventIDOf("event-start")
	startEvent := model.EventOf(
		startEventID, types.EventKindSessionStarted,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session started", startedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, startSession, startEvent); err != nil {
		t.Fatalf("SaveBoundary(start) error = %v", err)
	}

	// Simulate a concurrent "session label" operation that reads the current
	// aggregate, mutates only the label, and persists it.
	labelOpt, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID(label) error = %v", err)
	}
	labelled, _ := labelOpt.Value()
	labelled.SetLabel("sprint-1")
	if err := sessionDS.Save(ctx, labelled); err != nil {
		t.Fatalf("Save(label) error = %v", err)
	}

	// Now the "session end" path, which read the aggregate *before* the label
	// was applied. Under the old blind UPDATE this step would clobber the
	// label.
	endingSession := model.SessionOf(
		sessionID, startedAt, types.None[time.Time](),
		types.Client("cli"), agent, types.Workspace("workspace"),
		"", "", types.SessionID(""),
	)
	endedAt := startedAt.Add(time.Hour)
	if err := endingSession.End(endedAt, "wrapped up"); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	endEventID, _ := types.EventIDOf("event-end")
	endEvent := model.EventOf(
		endEventID, types.EventKindSessionEnded,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session ended", endedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, endingSession, endEvent); err != nil {
		t.Fatalf("SaveBoundary(end) error = %v", err)
	}

	after, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID(after end) error = %v", err)
	}
	got, _ := after.Value()
	if diff := cmp.Diff("sprint-1", got.Label()); diff != "" {
		t.Errorf("Label should be preserved across end (-want +got):\n%s", diff)
	}
	if _, ok := got.EndedAt().Value(); !ok {
		t.Fatalf("EndedAt() should be present after end")
	}
	if diff := cmp.Diff("wrapped up", got.Summary()); diff != "" {
		t.Errorf("Summary mismatch (-want +got):\n%s", diff)
	}
}

// TestSessionDatasource_SaveBoundary_DuplicateEndRejected asserts that a
// second session end on the same aggregate surfaces as
// model.ErrInvalidSessionState rather than silently overwriting ended_at.
func TestSessionDatasource_SaveBoundary_DuplicateEndRejected(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := infra.NewSessionDatasource(db)
	eventDS := infra.NewEventDatasource(db)

	sessionID, _ := types.SessionIDOf("session-double-end")
	agent, _ := types.AgentOf("claude")
	startedAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)

	startSession := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
	startEventID, _ := types.EventIDOf("event-start")
	startEvent := model.EventOf(
		startEventID, types.EventKindSessionStarted,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session started", startedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, startSession, startEvent); err != nil {
		t.Fatalf("SaveBoundary(start) error = %v", err)
	}

	// First end succeeds.
	firstEnding := model.SessionOf(
		sessionID, startedAt, types.None[time.Time](),
		types.Client("cli"), agent, types.Workspace("workspace"),
		"", "", types.SessionID(""),
	)
	firstEndedAt := startedAt.Add(time.Hour)
	if err := firstEnding.End(firstEndedAt, "first"); err != nil {
		t.Fatalf("End(first) error = %v", err)
	}
	firstEventID, _ := types.EventIDOf("event-end-1")
	firstEndEvent := model.EventOf(
		firstEventID, types.EventKindSessionEnded,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session ended", firstEndedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, firstEnding, firstEndEvent); err != nil {
		t.Fatalf("SaveBoundary(first end) error = %v", err)
	}

	// Second end races against the first. The aggregate it built locally
	// thinks the session is still open (ended_at empty), because it read
	// before the first end committed.
	secondEnding := model.SessionOf(
		sessionID, startedAt, types.None[time.Time](),
		types.Client("cli"), agent, types.Workspace("workspace"),
		"", "", types.SessionID(""),
	)
	secondEndedAt := startedAt.Add(2 * time.Hour)
	if err := secondEnding.End(secondEndedAt, "second"); err != nil {
		t.Fatalf("End(second) error = %v", err)
	}
	secondEventID, _ := types.EventIDOf("event-end-2")
	secondEndEvent := model.EventOf(
		secondEventID, types.EventKindSessionEnded,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session ended", secondEndedAt,
	)
	err := sessionDS.SaveBoundary(ctx, secondEnding, secondEndEvent)
	if err == nil {
		t.Fatalf("SaveBoundary(duplicate end) error = nil, want ErrInvalidSessionState")
	}
	if !errors.Is(err, model.ErrInvalidSessionState) {
		t.Fatalf("SaveBoundary(duplicate end) error = %v, want ErrInvalidSessionState", err)
	}

	// ended_at must still point at the first end, not the second.
	after, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	got, _ := after.Value()
	gotEndedAt, _ := got.EndedAt().Value()
	if !gotEndedAt.Equal(firstEndedAt) {
		t.Errorf("EndedAt() = %v, want %v (the first end must win)", gotEndedAt, firstEndedAt)
	}
	if diff := cmp.Diff("first", got.Summary()); diff != "" {
		t.Errorf("Summary should remain from first end (-want +got):\n%s", diff)
	}

	// The duplicate session_ended event must also have been rolled back —
	// only the first end event should be persisted.
	events, err := eventDS.ListRecent(ctx, 10, 0, types.EventKindSessionEnded, types.Client(""), types.Agent(""), sessionID, types.Workspace(""), false, time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ListRecent() len = %d, want 1 (duplicate end event must be rolled back)", len(events))
	}
	if diff := cmp.Diff("event-end-1", events[0].EventID().String()); diff != "" {
		t.Errorf("EventID mismatch (-want +got):\n%s", diff)
	}
}

// TestSessionDatasource_SaveBoundary_DuplicateStartRejected asserts that a
// second SaveBoundary(start) for the same session_id returns
// ErrInvalidSessionState and rolls back the duplicate session_started
// event. Without this guard a racing caller could commit two
// session_started events on the same session_id because the
// INSERT OR IGNORE session row update would silently no-op while the
// boundary event insert would still succeed in the caller's transaction.
func TestSessionDatasource_SaveBoundary_DuplicateStartRejected(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := infra.NewSessionDatasource(db)
	eventDS := infra.NewEventDatasource(db)

	sessionID, _ := types.SessionIDOf("session-double-start")
	agent, _ := types.AgentOf("claude")
	startedAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)

	firstSession := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
	firstEventID, _ := types.EventIDOf("event-start-1")
	firstEvent := model.EventOf(
		firstEventID, types.EventKindSessionStarted,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session started", startedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, firstSession, firstEvent); err != nil {
		t.Fatalf("SaveBoundary(first start) error = %v", err)
	}

	secondSession := model.NewSession(sessionID, startedAt.Add(time.Minute), types.Client("cli"), agent, types.Workspace("workspace"))
	secondEventID, _ := types.EventIDOf("event-start-2")
	secondEvent := model.EventOf(
		secondEventID, types.EventKindSessionStarted,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session started", startedAt.Add(time.Minute),
	)
	err := sessionDS.SaveBoundary(ctx, secondSession, secondEvent)
	if err == nil {
		t.Fatalf("SaveBoundary(second start) error = nil, want ErrInvalidSessionState")
	}
	if !errors.Is(err, model.ErrInvalidSessionState) {
		t.Fatalf("SaveBoundary(second start) error = %v, want ErrInvalidSessionState", err)
	}

	// Only the first session_started event must remain; the second must
	// have been rolled back together with the duplicate row attempt.
	events, err := eventDS.ListRecent(ctx, 10, 0, types.EventKindSessionStarted, types.Client(""), types.Agent(""), sessionID, types.Workspace(""), false, time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ListRecent() len = %d, want 1 (duplicate start event must be rolled back)", len(events))
	}
	if diff := cmp.Diff("event-start-1", events[0].EventID().String()); diff != "" {
		t.Errorf("EventID mismatch (-want +got):\n%s", diff)
	}
}

// TestSessionDatasource_Save_LabelOnEndedSession asserts that an ended
// session can still be labelled. Retroactively tagging a finished session
// is the common user workflow for organizing past work, so Save must not
// route the operation through the end-update guard.
func TestSessionDatasource_Save_LabelOnEndedSession(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := infra.NewSessionDatasource(db)

	sessionID, _ := types.SessionIDOf("session-label-after-end")
	agent, _ := types.AgentOf("claude")
	startedAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)

	startSession := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
	startEventID, _ := types.EventIDOf("event-start")
	startEvent := model.EventOf(
		startEventID, types.EventKindSessionStarted,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session started", startedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, startSession, startEvent); err != nil {
		t.Fatalf("SaveBoundary(start) error = %v", err)
	}

	// End the session.
	endingSession := model.SessionOf(
		sessionID, startedAt, types.None[time.Time](),
		types.Client("cli"), agent, types.Workspace("workspace"),
		"", "", types.SessionID(""),
	)
	endedAt := startedAt.Add(time.Hour)
	if err := endingSession.End(endedAt, "done"); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	endEventID, _ := types.EventIDOf("event-end")
	endEvent := model.EventOf(
		endEventID, types.EventKindSessionEnded,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session ended", endedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, endingSession, endEvent); err != nil {
		t.Fatalf("SaveBoundary(end) error = %v", err)
	}

	// Retroactively label the ended session (SessionUsecase.Label flow).
	loadedOpt, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	loaded, _ := loadedOpt.Value()
	loaded.SetLabel("retro-label")
	if err := sessionDS.Save(ctx, loaded); err != nil {
		t.Fatalf("Save(label on ended) error = %v", err)
	}

	after, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID(after label) error = %v", err)
	}
	got, _ := after.Value()
	if diff := cmp.Diff("retro-label", got.Label()); diff != "" {
		t.Errorf("Label mismatch (-want +got):\n%s", diff)
	}
	// ended_at must still point at the original end.
	if _, ok := got.EndedAt().Value(); !ok {
		t.Fatalf("EndedAt() should remain present after labelling")
	}
	gotEndedAt, _ := got.EndedAt().Value()
	if !gotEndedAt.Equal(endedAt) {
		t.Errorf("EndedAt() = %v, want %v", gotEndedAt, endedAt)
	}
	if diff := cmp.Diff("done", got.Summary()); diff != "" {
		t.Errorf("Summary should remain after labelling (-want +got):\n%s", diff)
	}
}

// TestSessionDatasource_Save_ClearLabel asserts that SetLabel("") clears the
// persisted label. Without this guarantee, callers cannot remove a label once
// it has been assigned.
func TestSessionDatasource_Save_ClearLabel(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := infra.NewSessionDatasource(db)

	sessionID, _ := types.SessionIDOf("session-clear-label")
	agent, _ := types.AgentOf("claude")
	startedAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)

	startSession := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
	startEventID, _ := types.EventIDOf("event-start")
	startEvent := model.EventOf(
		startEventID, types.EventKindSessionStarted,
		types.Client("cli"), agent, sessionID, types.Workspace("workspace"),
		"session started", startedAt,
	)
	if err := sessionDS.SaveBoundary(ctx, startSession, startEvent); err != nil {
		t.Fatalf("SaveBoundary(start) error = %v", err)
	}

	// Apply a label.
	labelOpt, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	labelled, _ := labelOpt.Value()
	labelled.SetLabel("sprint-1")
	if err := sessionDS.Save(ctx, labelled); err != nil {
		t.Fatalf("Save(label) error = %v", err)
	}

	// Clear it.
	storedOpt, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	stored, _ := storedOpt.Value()
	if got := stored.Label(); got != "sprint-1" {
		t.Fatalf("precondition: Label() = %q, want %q", got, "sprint-1")
	}
	stored.SetLabel("")
	if err := sessionDS.Save(ctx, stored); err != nil {
		t.Fatalf("Save(clear) error = %v", err)
	}

	after, err := sessionDS.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindByID(after clear) error = %v", err)
	}
	got, _ := after.Value()
	if diff := cmp.Diff("", got.Label()); diff != "" {
		t.Errorf("Label should be cleared (-want +got):\n%s", diff)
	}
}
