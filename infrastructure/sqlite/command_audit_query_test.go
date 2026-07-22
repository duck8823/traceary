package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestCommandAuditQueryListsStructuredReportWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	at := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	saveReportAudit(ctx, t, events, "rtk-success", "rtk git status", types.Some(0), types.CommandFailureReasonHostError, true, at)
	saveReportAudit(ctx, t, events, "timeout", "go test ./...", types.None[int](), types.CommandFailureReasonTimeout, false, at.Add(time.Minute))
	saveReportAudit(ctx, t, events, "outside", "npm test", types.Some(1), types.CommandFailureReasonExitCode, true, at.Add(24*time.Hour))

	criteria := apptypes.NewEventListCriteriaBuilder(1).
		Workspace("workspace").
		From(at.Add(-time.Minute)).
		To(at.Add(time.Hour)).
		Build()
	got, err := events.ListReportWindow(ctx, criteria)
	if err != nil {
		t.Fatalf("ListReportWindow() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListReportWindow() length = %d, want 2", len(got))
	}
	byID := map[types.EventID]apptypes.ReportCommandRecord{}
	for _, record := range got {
		byID[record.EventID] = record
	}
	success := byID["rtk-success"]
	if success.CommandName.String() != "git" || success.FailureReason != types.CommandFailureReasonNone || success.IsFailure() {
		t.Fatalf("rtk success = %#v", success)
	}
	if wrapper, ok := success.Wrapper.Value(); !ok || wrapper.String() != "rtk" {
		t.Fatalf("rtk wrapper = (%q, %v)", wrapper, ok)
	}
	timeout := byID["timeout"]
	if timeout.CommandName.String() != "go" || timeout.FailureReason != types.CommandFailureReasonTimeout || !timeout.IsFailure() {
		t.Fatalf("timeout = %#v", timeout)
	}
}

func saveReportAudit(
	ctx context.Context,
	t *testing.T,
	events interface {
		SaveWithAudit(context.Context, *model.Event, *model.CommandAudit) error
	},
	id, command string,
	exitCode types.Optional[int],
	reason types.CommandFailureReason,
	failed bool,
	at time.Time,
) {
	t.Helper()
	eventID := types.EventID(id)
	event := model.EventOf(eventID, types.EventKindCommandExecuted, "hook", "codex", "session", "workspace", command+"\n\nOUTPUT:\n{\"failed\": true}", at)
	audit, err := model.NewCommandAudit(eventID, command, "", `{"failed": true}`, false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit(%s) error = %v", id, err)
	}
	if err := audit.ClassifyOutcome(exitCode, reason, failed); err != nil {
		t.Fatalf("ClassifyOutcome(%s) error = %v", id, err)
	}
	if err := events.SaveWithAudit(ctx, event, audit); err != nil {
		t.Fatalf("SaveWithAudit(%s) error = %v", id, err)
	}
}
