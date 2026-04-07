package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type listEventsQueryServiceStub struct {
	receivedPath  string
	receivedLimit int
	called        bool
	events        []*model.Event
	err           error
}

func (s *listEventsQueryServiceStub) Run(
	_ context.Context,
	dbPath string,
	limit int,
) ([]*model.Event, error) {
	s.called = true
	s.receivedPath = dbPath
	s.receivedLimit = limit
	return s.events, s.err
}

var _ queryservice.ListRecentEventsQueryService = (*listEventsQueryServiceStub)(nil)

func TestRootCLI_ListCommand(t *testing.T) {
	t.Parallel()

	t.Run("イベント一覧を表示できる", func(t *testing.T) {
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

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
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
		rootCmd.SetArgs([]string{"list", "--db-path", dbPath, "--limit", "5"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !initStub.called {
			t.Fatalf("InitializeStoreUsecase.Run() was not called")
		}
		if !listStub.called {
			t.Fatalf("ListRecentEventsQueryService.Run() was not called")
		}
		if listStub.receivedPath != dbPath {
			t.Fatalf("dbPath = %q, want %q", listStub.receivedPath, dbPath)
		}
		if listStub.receivedLimit != 5 {
			t.Fatalf("limit = %d, want %d", listStub.receivedLimit, 5)
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

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
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
		rootCmd.SetArgs([]string{"list", "--db-path", dbPath, "--json"})

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

	t.Run("イベントがない場合はメッセージを表示する", func(t *testing.T) {
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
		if stdout.String() != "一致する記録はありません\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "一致する記録はありません\n")
		}
	})
}
