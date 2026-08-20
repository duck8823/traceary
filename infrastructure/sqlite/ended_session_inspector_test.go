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

func TestEndedSessionInspector_FindEndedSessionIDs(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := infra.NewDatabase(dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := infra.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	ds := infra.NewSessionDatasource(db)
	agent, err := types.AgentFrom("claude")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	startedAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	activeID := types.SessionID("active-session")
	endedID := types.SessionID("ended-session")
	for _, sessionID := range []types.SessionID{activeID, endedID} {
		session := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
		event := model.EventOf(types.EventID("start-"+sessionID.String()), types.EventKindSessionStarted, types.Client("cli"), agent, sessionID, types.Workspace("workspace"), "started", startedAt)
		if err := ds.SaveBoundary(ctx, session, event); err != nil {
			t.Fatalf("SaveBoundary(start %s) error = %v", sessionID, err)
		}
	}
	ended, err := ds.FindByID(ctx, endedID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	endedSession, ok := ended.Value()
	if !ok {
		t.Fatal("ended session is missing")
	}
	endedAt := startedAt.Add(time.Minute)
	if err := endedSession.End(endedAt, "done"); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	endEvent := model.EventOf(types.EventID("end-ended-session"), types.EventKindSessionEnded, types.Client("cli"), agent, endedID, types.Workspace("workspace"), "ended", endedAt)
	if err := ds.SaveBoundary(ctx, endedSession, endEvent); err != nil {
		t.Fatalf("SaveBoundary(end) error = %v", err)
	}

	inspector := infra.NewEndedSessionInspector()
	got, err := inspector.FindEndedSessionIDs(ctx, dbPath, []types.SessionID{activeID, endedID, "missing-session"})
	if err != nil {
		t.Fatalf("FindEndedSessionIDs() error = %v", err)
	}
	if diff := cmp.Diff(map[types.SessionID]struct{}{endedID: {}}, got); diff != "" {
		t.Fatalf("ended IDs mismatch (-want +got):\n%s", diff)
	}

	empty, err := inspector.FindEndedSessionIDs(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("FindEndedSessionIDs(nil) error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("FindEndedSessionIDs(nil) = %v, want empty", empty)
	}
}

func TestEndedSessionInspector_MissingStoreFails(t *testing.T) {
	t.Parallel()

	inspector := infra.NewEndedSessionInspector()
	if _, err := inspector.FindEndedSessionIDs(context.Background(), filepath.Join(t.TempDir(), "absent.db"), []types.SessionID{"s"}); err == nil {
		t.Fatal("FindEndedSessionIDs() on a missing store must fail instead of guessing session state")
	}
}
