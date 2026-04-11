package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type listEventsQueryServiceStub struct {
	receivedInput port.ListRecentEventsInput
	called        bool
	events        []*model.Event
	err           error
}

func (s *listEventsQueryServiceStub) Run(
	_ context.Context,
	input port.ListRecentEventsInput,
) ([]*model.Event, error) {
	s.called = true
	s.receivedInput = input
	return s.events, s.err
}

var _ queryservice.ListRecentEventsQueryService = (*listEventsQueryServiceStub)(nil)

func TestRootCLI_ListCommand(t *testing.T) {
	t.Parallel()

	t.Run("displays event list", func(t *testing.T) {
		t.Parallel()

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

		initStub := &initializeStoreUsecaseStub{}
		listStub := &listEventsQueryServiceStub{
			events: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello",
					time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			ListEventsQueryService: listStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"list",
			"--db-path",
		"/tmp/test-traceary.db",
			"--limit", "5",
			"--offset", "2",
			"--kind", "note",
			"--client", "cli",
			"--agent", "codex",
			"--session-id", "session-1",
			"--repo", "duck8823/traceary",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !initStub.called {
			t.Fatalf("InitializeStoreUsecase.Run() was not called")
		}
		if !listStub.called {
			t.Fatalf("ListRecentEventsQueryService.Run() was not called")
		}
		if listStub.receivedInput.Limit != 5 {
			t.Fatalf("limit = %d, want %d", listStub.receivedInput.Limit, 5)
		}
		if listStub.receivedInput.Offset != 2 {
			t.Fatalf("offset = %d, want %d", listStub.receivedInput.Offset, 2)
		}
		if listStub.receivedInput.Kind != "note" {
			t.Fatalf("kind = %q, want %q", listStub.receivedInput.Kind, "note")
		}
		if listStub.receivedInput.SessionID != "session-1" {
			t.Fatalf("session_id = %q, want %q", listStub.receivedInput.SessionID, "session-1")
		}
		want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tREPO\tMESSAGE\n" +
			"2026-04-07T12:00:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\thello\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("JSON 形式でイベント一覧を表示できる", func(t *testing.T) {
		t.Parallel()

		eventID, err := types.EventIDOf("event-2")
		if err != nil {
			t.Fatalf("EventIDOf() error = %v", err)
		}
		agent, err := types.AgentOf("codex")
		if err != nil {
			t.Fatalf("AgentOf() error = %v", err)
		}
		sessionID, err := types.SessionIDOf("session-2")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}

		initStub := &initializeStoreUsecaseStub{}
		listStub := &listEventsQueryServiceStub{
			events: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello json",
					time.Date(2026, 4, 7, 12, 30, 0, 0, time.UTC),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			ListEventsQueryService: listStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--json"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		want := "" +
			"[\n" +
			"  {\n" +
			"    \"event_id\": \"event-2\",\n" +
			"    \"kind\": \"note\",\n" +
			"    \"client\": \"cli\",\n" +
			"    \"agent\": \"codex\",\n" +
			"    \"session_id\": \"session-2\",\n" +
			"    \"repo\": \"duck8823/traceary\",\n" +
			"    \"message\": \"hello json\",\n" +
			"    \"created_at\": \"2026-04-07T12:30:00Z\"\n" +
			"  }\n" +
			"]\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("displays message when no events exist", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		initStub := &initializeStoreUsecaseStub{}
		listStub := &listEventsQueryServiceStub{}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			ListEventsQueryService: listStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "No matching records.\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "No matching records.\n")
		}
	})

	t.Run("offset が負ならエラー", func(t *testing.T) {
		t.Parallel()

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: &initializeStoreUsecaseStub{},
			ListEventsQueryService: &listEventsQueryServiceStub{},
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--offset", "-1"})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
	})

	t.Run("kind が不正ならエラー", func(t *testing.T) {
		t.Parallel()

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: &initializeStoreUsecaseStub{},
			ListEventsQueryService: &listEventsQueryServiceStub{},
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--kind", "unknown"})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
	})

	t.Run("--kind audit resolves to command_executed", func(t *testing.T) {
		t.Parallel()

		listStub := &listEventsQueryServiceStub{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: &initializeStoreUsecaseStub{},
			ListEventsQueryService: listStub,
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--kind", "audit"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !listStub.called {
			t.Fatal("ListRecentEventsQueryService.Run() was not called")
		}
		if listStub.receivedInput.Kind != "command_executed" {
			t.Fatalf("Kind = %q, want %q", listStub.receivedInput.Kind, "command_executed")
		}
	})
}
