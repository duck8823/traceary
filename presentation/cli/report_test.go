package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_Report_SucceedsWithJSON(t *testing.T) {
	t.Parallel()

	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom: %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-report-1")
	if err != nil {
		t.Fatalf("SessionIDFrom: %v", err)
	}
	eventID, err := types.EventIDFrom("event-report-1")
	if err != nil {
		t.Fatalf("EventIDFrom: %v", err)
	}
	event := model.EventOf(
		eventID,
		types.EventKindCommandExecuted,
		"hook",
		agent,
		sessionID,
		"duck8823/traceary",
		"go test ./...\n\nINPUT:\n\n\nOUTPUT:\nok",
		time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
	)
	summary := apptypes.SessionSummaryOf(
		sessionID,
		types.Workspace("duck8823/traceary"),
		time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC),
		types.None[time.Time](),
		"active",
		1,
		1,
		[]string{"codex"},
		"",
		"",
		types.SessionID(""),
		types.Client("hook"),
	)
	sessionStub := &sessionUsecaseStub{listResult: []apptypes.SessionSummary{summary}}
	eventStub := &eventUsecaseStub{listEvents: []*model.Event{event}}
	reportStub := &reportCommandUsecaseStub{summary: apptypes.ReportCommandSummary{
		FailuresByClient: map[string]int{}, FailuresByReason: map[string]int{},
		TopCommands: []apptypes.ReportCommandRow{{Command: "go", Count: 1, SampleEventID: eventID.String()}},
	}}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithEvent(eventStub),
		cli.WithReportCommand(reportStub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"report",
		"--db-path", "/tmp/test-traceary.db",
		"--from", "2026-07-01",
		"--to", "2026-07-16",
		"--json",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, `"period"`) || !strings.Contains(out, `"sessions"`) {
		t.Fatalf("JSON missing expected keys: %s", out)
	}
	if !strings.Contains(out, `"top_commands"`) {
		t.Fatalf("JSON missing top_commands: %s", out)
	}
	if !strings.Contains(out, "go") {
		t.Fatalf("JSON missing top command token: %s", out)
	}
	var document struct {
		Period struct {
			RequestedFrom          string `json:"requested_from"`
			RequestedTo            string `json:"requested_to"`
			EffectiveFromInclusive string `json:"effective_from_inclusive"`
			EffectiveToExclusive   string `json:"effective_to_exclusive"`
			Timezone               string `json:"timezone"`
			SnapshotAt             string `json:"snapshot_at"`
		} `json:"period"`
	}
	if err := json.Unmarshal([]byte(out), &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if document.Period.RequestedFrom != "2026-07-01" || document.Period.RequestedTo != "2026-07-16" {
		t.Fatalf("requested period = %+v", document.Period)
	}
	if document.Period.EffectiveFromInclusive != "2026-07-01T00:00:00Z" || document.Period.EffectiveToExclusive != "2026-07-17T00:00:00Z" {
		t.Fatalf("effective period = %+v", document.Period)
	}
	if document.Period.Timezone != "UTC" || document.Period.SnapshotAt == "" {
		t.Fatalf("period metadata = %+v", document.Period)
	}
	if !reportStub.criteria.From().Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) || !reportStub.criteria.To().Equal(time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("report criteria = [%s, %s)", reportStub.criteria.From(), reportStub.criteria.To())
	}
}

func TestRootCLI_Report_TextExitZero(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
		cli.WithReportCommand(&reportCommandUsecaseStub{summary: apptypes.ReportCommandSummary{FailuresByClient: map[string]int{}, FailuresByReason: map[string]int{}}}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"report",
		"--db-path", "/tmp/test-traceary.db",
		"--from", "2026-07-01T00:00:00Z",
		"--to", "2026-07-16T00:00:00Z",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute error = %v (success path must exit 0)", err)
	}
	if !strings.Contains(stdout.String(), "Traceary report") {
		t.Fatalf("text missing header: %s", stdout.String())
	}
}

func TestRootCLI_Report_TextPreservesRequestedCalendarEnd(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
		cli.WithReportCommand(&reportCommandUsecaseStub{summary: apptypes.ReportCommandSummary{FailuresByClient: map[string]int{}, FailuresByReason: map[string]int{}}}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"report", "--db-path", "/tmp/test-traceary.db",
		"--from", "2026-03-08", "--to", "2026-03-08",
		"--timezone", "America/New_York",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Period: 2026-03-08 → 2026-03-08 (inclusive calendar end; timezone=America/New_York)") {
		t.Fatalf("text must preserve requested calendar end: %s", out)
	}
	if !strings.Contains(out, "Effective interval: 2026-03-08T05:00:00Z → 2026-03-09T04:00:00Z") {
		t.Fatalf("text missing DST-safe effective interval: %s", out)
	}
}
