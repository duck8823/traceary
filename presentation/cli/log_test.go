package cli_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/port"
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

	t.Run("records log with flag values", func(t *testing.T) {
		t.Parallel()

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
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			RecordLogUsecase:       logStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(stderr)
		rootCmd.SetArgs([]string{
			"log",
			"--db-path",
		"/tmp/test-traceary.db",
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
			if logStub.receivedInput.Agent != "codex" {
			t.Fatalf("Agent = %q, want %q", logStub.receivedInput.Agent, "codex")
		}
		if logStub.receivedInput.Client != "cli" {
			t.Fatalf("Client = %q, want %q", logStub.receivedInput.Client, "cli")
		}
		if stdout.String() != "Recorded: event-1\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "Recorded: event-1\n")
		}
	})

	t.Run("uses environment variable defaults", func(t *testing.T) {
		t.Setenv("TRACEARY_AGENT", "claude")
		t.Setenv("TRACEARY_SESSION_ID", "session-env")
		t.Setenv("TRACEARY_CLIENT", "hook")
		t.Setenv("TRACEARY_REPO", "duck8823/traceary")

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
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			RecordLogUsecase:       logStub,
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "hello"})

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

	t.Run("id-only で event ID だけを出力できる", func(t *testing.T) {
		t.Parallel()

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
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			RecordLogUsecase:       logStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "--id-only", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "event-1\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "event-1\n")
		}
	})

	t.Run("json で構造化出力できる", func(t *testing.T) {
		t.Parallel()

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
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			RecordLogUsecase:       logStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "--session-id", "session-1", "--json", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		payload := decodeJSONMap(t, stdout.String())
		if got, want := payload["event_id"], "event-1"; got != want {
			t.Fatalf("event_id = %v, want %q", got, want)
		}
		if got, want := payload["message"], "hello"; got != want {
			t.Fatalf("message = %v, want %q", got, want)
		}
	})

	t.Run("active session を既定利用できる", func(t *testing.T) {
		t.Setenv("TRACEARY_SESSION_ID", "")
		cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
			return "github.com/duck8823/traceary", nil
		})
		defer cli.ResetDetectRepoContextFunc()

		activeEventID, err := types.EventIDOf("event-session-start")
		if err != nil {
			t.Fatalf("EventIDOf() error = %v", err)
		}
		activeSessionID, err := types.SessionIDOf("session-active")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}

		initStub := &initializeStoreUsecaseStub{}
		queryStub := &findLatestSessionQueryServiceStub{
			event: model.EventOf(
				activeEventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				activeSessionID,
				"github.com/duck8823/traceary",
				"session started",
				time.Now().Add(-1*time.Hour),
			),
		}
		logStub := &recordLogUsecaseStub{
			event: model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				activeSessionID,
				"github.com/duck8823/traceary",
				"hello",
				fixedLogTime(),
			),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:        initStub,
			RecordLogUsecase:              logStub,
			FindLatestSessionQueryService: queryStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !queryStub.called {
			t.Fatal("FindLatestSessionQueryService.Run() was not called")
		}
		if queryStub.receivedInput != (port.FindLatestSessionInput{
			Repo:       "github.com/duck8823/traceary",
			ActiveOnly: true,
		}) {
			t.Fatalf("receivedInput = %+v", queryStub.receivedInput)
		}
		if logStub.receivedInput.SessionID != "session-active" {
			t.Fatalf("SessionID = %q, want %q", logStub.receivedInput.SessionID, "session-active")
		}
		want := "" +
			"Using active session: session-active\n" +
			"Recorded: event-1\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("work context が無い場合は default session に fallback する", func(t *testing.T) {
		t.Setenv("TRACEARY_SESSION_ID", "")
		cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
			return "", errors.New("no git remote")
		})
		defer cli.ResetDetectRepoContextFunc()

		initStub := &initializeStoreUsecaseStub{}
		queryStub := &findLatestSessionQueryServiceStub{}
		logStub := &recordLogUsecaseStub{
			event: model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				mustSessionID(t, "default"),
				"",
				"hello",
				fixedLogTime(),
			),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:        initStub,
			RecordLogUsecase:              logStub,
			FindLatestSessionQueryService: queryStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if queryStub.called {
			t.Fatal("FindLatestSessionQueryService.Run() should not have been called")
		}
		if logStub.receivedInput.SessionID != "default" {
			t.Fatalf("SessionID = %q, want %q", logStub.receivedInput.SessionID, "default")
		}
		want := "" +
			"No work context was detected; using default session ID\n" +
			"Recorded: event-1\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})
}

func fixedLogTime() time.Time {
	return time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
}

func mustSessionID(t *testing.T, value string) types.SessionID {
	t.Helper()

	sessionID, err := types.SessionIDOf(value)
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	return sessionID
}
