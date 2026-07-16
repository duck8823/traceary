package cli_test

import (
	"bytes"
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
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithEvent(eventStub),
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
}

func TestRootCLI_Report_TextExitZero(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
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
