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

type recordLogUsecaseStub struct {
	receivedInput usecase.RecordLogInput
	called        bool
	event         *model.Event
	err           error
}

func (s *recordLogUsecaseStub) Run(_ context.Context, input usecase.RecordLogInput) (*model.Event, error) {
	s.called = true
	s.receivedInput = input
	return s.event, s.err
}

var _ usecase.RecordLogUsecase = (*recordLogUsecaseStub)(nil)

func TestRootCLI_LogCommand(t *testing.T) {
	eventID, err := types.EventIDOf("event-1")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-1")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	t.Run("フラグ値でログを記録できる", func(t *testing.T) {
		t.Parallel()

		dbPath := t.TempDir() + "/traceary.db"
		initStub := &initializeStoreUsecaseStub{}
		logStub := &recordLogUsecaseStub{
			event: model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"hello",
				fixedLogTime(),
			),
		}
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(initStub, logStub, nil, nil, nil).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(stderr)
		rootCmd.SetArgs([]string{
			"log",
			"--db-path", dbPath,
			"--client", "cli",
			"--agent", "codex",
			"--session-id", "session-1",
			"--repo", "duck8823/traceary",
			"hello",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !initStub.called {
			t.Fatalf("InitializeStoreUsecase.Run() was not called")
		}
		if !logStub.called {
			t.Fatalf("RecordLogUsecase.Run() was not called")
		}
		if logStub.receivedInput.DBPath != dbPath {
			t.Fatalf("DBPath = %q, want %q", logStub.receivedInput.DBPath, dbPath)
		}
		if logStub.receivedInput.Agent != "codex" {
			t.Fatalf("Agent = %q, want %q", logStub.receivedInput.Agent, "codex")
		}
		if logStub.receivedInput.Client != "cli" {
			t.Fatalf("Client = %q, want %q", logStub.receivedInput.Client, "cli")
		}
		if stdout.String() != "記録しました: event-1\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "記録しました: event-1\n")
		}
	})

	t.Run("環境変数デフォルトを利用できる", func(t *testing.T) {
		t.Setenv("TRACEARY_AGENT", "claude")
		t.Setenv("TRACEARY_SESSION_ID", "session-env")
		t.Setenv("TRACEARY_CLIENT", "hook")
		t.Setenv("TRACEARY_REPO", "duck8823/traceary")

		dbPath := t.TempDir() + "/traceary.db"
		initStub := &initializeStoreUsecaseStub{}
		logStub := &recordLogUsecaseStub{
			event: model.EventOf(
				eventID,
				types.EventKindNote,
				"hook",
				agent,
				sessionID,
				"duck8823/traceary",
				"hello",
				fixedLogTime(),
			),
		}
		rootCmd := cli.NewRootCLI(initStub, logStub, nil, nil, nil).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", dbPath, "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if logStub.receivedInput.Agent != "claude" {
			t.Fatalf("Agent = %q, want %q", logStub.receivedInput.Agent, "claude")
		}
		if logStub.receivedInput.SessionID != "session-env" {
			t.Fatalf("SessionID = %q, want %q", logStub.receivedInput.SessionID, "session-env")
		}
		if logStub.receivedInput.Client != "hook" {
			t.Fatalf("Client = %q, want %q", logStub.receivedInput.Client, "hook")
		}
		if logStub.receivedInput.Repo != "duck8823/traceary" {
			t.Fatalf("Repo = %q, want %q", logStub.receivedInput.Repo, "duck8823/traceary")
		}
	})
}

func fixedLogTime() time.Time {
	return time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
}
