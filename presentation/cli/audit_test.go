package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type recordCommandAuditUsecaseStub struct {
	receivedInput usecase.RecordCommandAuditInput
	called        bool
	event         *model.Event
	commandAudit  *model.CommandAudit
	err           error
}

func (s *recordCommandAuditUsecaseStub) Run(
	_ context.Context,
	input usecase.RecordCommandAuditInput,
) (*model.Event, *model.CommandAudit, error) {
	s.called = true
	s.receivedInput = input
	return s.event, s.commandAudit, s.err
}

var _ usecase.RecordCommandAuditUsecase = (*recordCommandAuditUsecaseStub)(nil)

func TestRootCLI_AuditCommand(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDOf("event-1")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-env")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"go test ./...",
		"stdin",
		"stdout",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	initStub := &initializeStoreUsecaseStub{}
	auditStub := &recordCommandAuditUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"go test ./...",
			time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
		),
		commandAudit: commandAudit,
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(initStub, nil, nil, auditStub, nil, nil).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"--repo", "duck8823/traceary",
		"go test ./...",
		"stdin",
		"stdout",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !auditStub.called {
		t.Fatalf("RecordCommandAuditUsecase.Run() was not called")
	}
	if auditStub.receivedInput.Command != "go test ./..." {
		t.Fatalf("Command = %q, want %q", auditStub.receivedInput.Command, "go test ./...")
	}
	if auditStub.receivedInput.SessionID != "session-env" {
		t.Fatalf("SessionID = %q, want %q", auditStub.receivedInput.SessionID, "session-env")
	}
	if stdout.String() != "記録しました: event-1\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "記録しました: event-1\n")
	}
}
