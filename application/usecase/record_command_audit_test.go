package usecase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

type commandAuditSaverStub struct {
	receivedPath      string
	savedEvent        *model.Event
	savedCommandAudit *model.CommandAudit
	err               error
}

func (s *commandAuditSaverStub) SaveCommandAudit(
	_ context.Context,
	dbPath string,
	event *model.Event,
	commandAudit *model.CommandAudit,
) error {
	s.receivedPath = dbPath
	s.savedEvent = event
	s.savedCommandAudit = commandAudit
	return s.err
}

func TestRecordCommandAuditUsecase_Run(t *testing.T) {
	t.Parallel()

	t.Run("監査イベントを保存できる", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)

		event, commandAudit, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:    "/tmp/traceary.db",
			Command:   "go test ./...",
			Input:     "stdin",
			Output:    "stdout",
			Client:    "cli",
			Agent:     "codex",
			SessionID: "session-1",
			Repo:      "duck8823/traceary",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if event == nil || commandAudit == nil {
			t.Fatalf("Run() returned nil values")
		}
		if stub.receivedPath != "/tmp/traceary.db" {
			t.Fatalf("received path = %q, want %q", stub.receivedPath, "/tmp/traceary.db")
		}
		if stub.savedEvent != event {
			t.Fatalf("saved event mismatch")
		}
		if stub.savedCommandAudit != commandAudit {
			t.Fatalf("saved command audit mismatch")
		}
		if event.Kind().String() != "command_executed" {
			t.Fatalf("Kind() = %q, want %q", event.Kind(), "command_executed")
		}
		if event.Body() != "go test ./..." {
			t.Fatalf("Body() = %q, want %q", event.Body(), "go test ./...")
		}
	})

	t.Run("長い input/output は切り詰めて保存する", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)
		longInput := strings.Repeat("i", 70*1024)
		longOutput := strings.Repeat("o", 70*1024)

		_, commandAudit, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:    "/tmp/traceary.db",
			Command:   "go test ./...",
			Input:     longInput,
			Output:    longOutput,
			Client:    "cli",
			Agent:     "codex",
			SessionID: "session-1",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if !commandAudit.InputTruncated() {
			t.Fatalf("InputTruncated() = false, want true")
		}
		if !commandAudit.OutputTruncated() {
			t.Fatalf("OutputTruncated() = false, want true")
		}
		if !strings.HasSuffix(commandAudit.Input(), "\n...[truncated]") {
			t.Fatalf("Input() suffix = %q, want truncated suffix", commandAudit.Input()[len(commandAudit.Input())-16:])
		}
		if !strings.HasSuffix(commandAudit.Output(), "\n...[truncated]") {
			t.Fatalf("Output() suffix = %q, want truncated suffix", commandAudit.Output()[len(commandAudit.Output())-16:])
		}
	})
}
