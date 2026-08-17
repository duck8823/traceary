package usecase_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	domaintypes "github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func newParentEndChildrenTestDatabase(t *testing.T) *sqliteinfra.Database {
	t.Helper()

	ctx := context.Background()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	migrations := os.DirFS(filepath.Join(filepath.Dir(sourceFile), "..", "..", "schema", "sqlite", "migrations"))
	database := sqliteinfra.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), migrations)
	if err := sqliteinfra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return database
}

// TestSessionUsecase_End_ClosesOpenChildSessions drives the real
// SessionUsecase.End against a fixture DB (parent + still-open child), and
// asserts the child no longer leaks as an open session that
// Active() would keep returning after its parent ended
// (#2012).
func TestSessionUsecase_End_ClosesOpenChildSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	database := newParentEndChildrenTestDatabase(t)
	sessions := sqliteinfra.NewSessionDatasource(database)
	sut := usecase.NewSessionUsecase(nil, sessions, sessions, nil)

	parentEvent, err := sut.Start(ctx, "cli", "claude", "parent-session", "workspace", "")
	if err != nil {
		t.Fatalf("Start(parent) error = %v", err)
	}

	childEvent, err := sut.StartChild(
		ctx,
		"parent-session",
		"child-session",
		"claude",
		"workspace",
		parentEvent.EventID(),
		"general-purpose",
		parentEvent.CreatedAt(),
	)
	if err != nil {
		t.Fatalf("StartChild() error = %v", err)
	}
	if childEvent == nil {
		t.Fatal("StartChild() returned nil event")
	}

	if _, err := sut.End(ctx, "cli", "claude", "parent-session", "workspace", ""); err != nil {
		t.Fatalf("End(parent) error = %v", err)
	}

	child, err := sessions.FindByID(ctx, domaintypes.SessionID("child-session"))
	if err != nil {
		t.Fatalf("FindByID(child) error = %v", err)
	}
	childSession, ok := child.Value()
	if !ok {
		t.Fatal("FindByID(child) returned no session")
	}
	if _, ended := childSession.EndedAt().Value(); !ended {
		t.Fatal("child session EndedAt() is not set; want the child to be closed when the parent ends")
	}

	// `find_active_session.sql` ranks active candidates by start time, so a
	// leaked open child (started before the parent ended) keeps winning
	// Active() for this (client, agent, workspace) bucket
	// for as long as it stays open, even though the parent it belonged to
	// has already finished. Once the child is closed too, nothing in this
	// bucket should report as active.
	criteria := types.NewSessionLookupCriteriaBuilder().
		Client("cli").
		Agent("claude").
		Workspace("workspace").
		ActiveOnly(true).
		Build()
	active, err := sut.Active(ctx, criteria)
	if err != nil {
		t.Fatalf("Active() error = %v", err)
	}
	if activeEvent, ok := active.Value(); ok {
		t.Fatalf("Active() session ID = %q, want no active session (leaked child must not shadow the ended parent)", activeEvent.SessionID())
	}
}
