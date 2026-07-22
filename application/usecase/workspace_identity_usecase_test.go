package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domainTypes "github.com/duck8823/traceary/domain/types"
)

type workspaceIdentityStub struct {
	report  types.WorkspaceIdentityReport
	limit   int
	saved   *model.WorkspaceAlias
	deleted [2]string
}

func (s *workspaceIdentityStub) WorkspaceIdentityReport(_ context.Context, limit int) (types.WorkspaceIdentityReport, error) {
	s.limit = limit
	return s.report, nil
}
func (s *workspaceIdentityStub) SaveWorkspaceAlias(_ context.Context, alias *model.WorkspaceAlias) error {
	s.saved = alias
	return nil
}
func (s *workspaceIdentityStub) DeleteWorkspaceAlias(_ context.Context, sessionID domainTypes.SessionID, workspace domainTypes.Workspace) error {
	s.deleted = [2]string{sessionID.String(), workspace.String()}
	return nil
}

type workspaceIdentityClock struct{ now time.Time }

func (c workspaceIdentityClock) Now() time.Time { return c.now }

func TestWorkspaceIdentityUsecase_ReportsAndManagesReviewedAliases(t *testing.T) {
	t.Parallel()
	stub := &workspaceIdentityStub{report: types.WorkspaceIdentityReport{Coverage: types.WorkspaceIdentityCoverage{EventCount: 3}}}
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	sut := usecase.NewWorkspaceIdentityUsecase(stub, stub, workspaceIdentityClock{now: now})

	report, err := sut.Report(context.Background(), 7)
	if err != nil || report.Coverage.EventCount != 3 || stub.limit != 7 {
		t.Fatalf("Report() = %#v, %v; limit=%d", report, err, stub.limit)
	}
	if err := sut.AddAlias(context.Background(), domainTypes.SessionID("session-1"), domainTypes.Workspace(" /repo "), " operator ", "reviewed"); err != nil {
		t.Fatalf("AddAlias() error = %v", err)
	}
	if stub.saved == nil || stub.saved.Workspace().String() != "/repo" || stub.saved.ReviewedAt() != now {
		t.Fatalf("saved alias = %#v", stub.saved)
	}
	if err := sut.RemoveAlias(context.Background(), domainTypes.SessionID("session-1"), domainTypes.Workspace(" /repo ")); err != nil {
		t.Fatalf("RemoveAlias() error = %v", err)
	}
	if stub.deleted != [2]string{"session-1", "/repo"} {
		t.Fatalf("deleted = %#v", stub.deleted)
	}
}

func TestWorkspaceIdentityUsecase_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	stub := &workspaceIdentityStub{}
	sut := usecase.NewWorkspaceIdentityUsecase(stub, stub, workspaceIdentityClock{now: time.Now()})
	if _, err := sut.Report(context.Background(), -1); err == nil {
		t.Fatal("Report(-1) error = nil")
	}
	if err := sut.AddAlias(context.Background(), "", "/repo", "operator", ""); err == nil {
		t.Fatal("AddAlias(empty session) error = nil")
	}
	if err := sut.RemoveAlias(context.Background(), "session-1", " "); err == nil {
		t.Fatal("RemoveAlias(empty workspace) error = nil")
	}
}
