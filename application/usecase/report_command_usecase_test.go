package usecase_test

import (
	"context"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

type reportCommandQueryStub struct {
	records  []apptypes.ReportCommandRecord
	criteria apptypes.EventListCriteria
}

func (s *reportCommandQueryStub) ListReportWindow(_ context.Context, criteria apptypes.EventListCriteria) ([]apptypes.ReportCommandRecord, error) {
	s.criteria = criteria
	return s.records, nil
}

func TestReportCommandUsecaseSummarizesStructuredIdentityAndFailures(t *testing.T) {
	t.Parallel()
	stub := &reportCommandQueryStub{records: []apptypes.ReportCommandRecord{
		reportCommandRecord("direct-success", "git", types.CommandFailureReasonNone, types.Some(0), false),
		reportCommandRecord("rtk-quoted-text", "git", types.CommandFailureReasonNone, types.Some(0), true),
		reportCommandRecord("failure-1", "go", types.CommandFailureReasonExitCode, types.Some(1), true),
		reportCommandRecord("failure-2", "go", types.CommandFailureReasonTimeout, types.None[int](), true),
		reportCommandRecord("failure-3", "go", types.CommandFailureReasonHookDenied, types.None[int](), true),
	}}
	sut := usecase.NewReportCommandUsecase(stub)
	criteria := apptypes.NewEventListCriteriaBuilder(2).Workspace("workspace").Build()
	got, err := sut.Summarize(context.Background(), criteria)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if len(got.TopCommands) != 2 || got.TopCommands[0].Command != "go" || got.TopCommands[0].FailedCount != 3 || got.TopCommands[1].Command != "git" || got.TopCommands[1].FailedCount != 0 {
		t.Fatalf("TopCommands = %#v", got.TopCommands)
	}
	if got.FailureTotal != 3 {
		t.Fatalf("FailureTotal = %d, want 3", got.FailureTotal)
	}
	if got.FailuresByReason["exit_code"] != 1 || got.FailuresByReason["timeout"] != 1 || got.FailuresByReason["hook_denied"] != 1 {
		t.Fatalf("FailuresByReason = %#v", got.FailuresByReason)
	}
	if len(got.FailureLoops) != 1 || got.FailureLoops[0].Command != "go" || got.FailureLoops[0].Count != 3 {
		t.Fatalf("FailureLoops = %#v", got.FailureLoops)
	}
	if stub.criteria.Workspace() != "workspace" {
		t.Fatalf("query workspace = %q", stub.criteria.Workspace())
	}
}

func reportCommandRecord(id, command string, reason types.CommandFailureReason, exitCode types.Optional[int], failed bool) apptypes.ReportCommandRecord {
	return apptypes.ReportCommandRecord{
		EventID: types.EventID(id), Client: "hook", Agent: "codex", Workspace: "workspace",
		CommandName: types.CommandName(command), ExitCode: exitCode, Failed: failed,
		FailureReason: reason, CreatedAt: time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC),
	}
}
