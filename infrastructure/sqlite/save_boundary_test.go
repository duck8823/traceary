package sqlite_test

import (
	"context"
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
	if !stored.IsPresent() {
		t.Fatalf("FindByID() should be present after SaveBoundary")
	}

	// Event row was inserted
	events, err := eventDS.ListRecent(ctx, 10, 0, types.EventKindSessionStarted, types.Client(""), types.Agent(""), sessionID, types.Workspace(""), false, time.Time{}, time.Time{})
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
	storedSession, _ := storedOpt.Get()
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
	updated, _ := updatedOpt.Get()
	if !updated.EndedAt().IsPresent() {
		t.Fatalf("EndedAt() should be present after end")
	}
	gotEndedAt, _ := updated.EndedAt().Get()
	if !gotEndedAt.Equal(endedAt) {
		t.Errorf("EndedAt() = %v, want %v", gotEndedAt, endedAt)
	}
	if diff := cmp.Diff("wrapped up", updated.Summary()); diff != "" {
		t.Errorf("Summary mismatch (-want +got):\n%s", diff)
	}
}
