package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type searchEventsQueryServiceStub struct {
	receivedInput port.SearchEventsInput
	called        bool
	events        []*model.Event
	err           error
}

func (s *searchEventsQueryServiceStub) Run(
	_ context.Context,
	input port.SearchEventsInput,
) ([]*model.Event, error) {
	s.called = true
	s.receivedInput = input
	return s.events, s.err
}

var _ queryservice.SearchEventsQueryService = (*searchEventsQueryServiceStub)(nil)

func TestRootCLI_SearchCommand(t *testing.T) {
	t.Setenv("TRACEARY_REPO", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

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
	searchStub := &searchEventsQueryServiceStub{
		events: []*model.Event{
			model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				sessionID,
				"github.com/duck8823/traceary",
				"hello traceary",
				time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
			),
		},
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:   initStub,
		SearchEventsQueryService: searchStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--session-id", "session-1",
		"--client", "cli",
		"--agent", "codex",
		"--kind", "note",
		"--from", "2026-04-07",
		"--since", "2026-04-07",
		"--to", "2026-04-07",
		"--until", "2026-04-07",
		"--limit", "5",
		"--offset", "2",
		"traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !searchStub.called {
		t.Fatalf("SearchEventsQueryService.Run() was not called")
	}
	if searchStub.receivedInput.Query != "traceary" {
		t.Fatalf("Query = %q, want %q", searchStub.receivedInput.Query, "traceary")
	}
	if searchStub.receivedInput.Repo != "github.com/duck8823/traceary" {
		t.Fatalf("Repo = %q, want %q", searchStub.receivedInput.Repo, "github.com/duck8823/traceary")
	}
	if searchStub.receivedInput.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want %q", searchStub.receivedInput.SessionID, "session-1")
	}
	if searchStub.receivedInput.Client != "cli" {
		t.Fatalf("Client = %q, want %q", searchStub.receivedInput.Client, "cli")
	}
	if searchStub.receivedInput.Agent != "codex" {
		t.Fatalf("Agent = %q, want %q", searchStub.receivedInput.Agent, "codex")
	}
	if searchStub.receivedInput.Kind != "note" {
		t.Fatalf("Kind = %q, want %q", searchStub.receivedInput.Kind, "note")
	}
	if searchStub.receivedInput.Offset != 2 {
		t.Fatalf("Offset = %d, want %d", searchStub.receivedInput.Offset, 2)
	}
	if stdout.String() == "" {
		t.Fatalf("stdout is empty")
	}
}

func TestRootCLI_SearchCommand_JSON(t *testing.T) {
	t.Setenv("TRACEARY_REPO", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

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
	searchStub := &searchEventsQueryServiceStub{
		events: []*model.Event{
			model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				sessionID,
				"github.com/duck8823/traceary",
				"hello json search",
				time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
			),
		},
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:   initStub,
		SearchEventsQueryService: searchStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--json",
		"traceary",
	})

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
		"    \"repo\": \"github.com/duck8823/traceary\",\n" +
		"    \"message\": \"hello json search\",\n" +
		"    \"created_at\": \"2026-04-07T13:00:00Z\"\n" +
		"  }\n" +
		"]\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRootCLI_SearchCommand_FilterOnly(t *testing.T) {
	t.Setenv("TRACEARY_REPO", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	initStub := &initializeStoreUsecaseStub{}
	searchStub := &searchEventsQueryServiceStub{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:   initStub,
		SearchEventsQueryService: searchStub,
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--session-id", "session-42",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !searchStub.called {
		t.Fatalf("SearchEventsQueryService.Run() was not called")
	}
	if searchStub.receivedInput.Query != "" {
		t.Fatalf("Query = %q, want empty", searchStub.receivedInput.Query)
	}
	if searchStub.receivedInput.SessionID != "session-42" {
		t.Fatalf("SessionID = %q, want %q", searchStub.receivedInput.SessionID, "session-42")
	}
}

func TestRootCLI_SearchCommand_NegativeOffset(t *testing.T) {
	t.Setenv("TRACEARY_REPO", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
		SearchEventsQueryService: &searchEventsQueryServiceStub{},
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--offset", "-1",
		"traceary",
	})

	if err := rootCmd.Execute(); err == nil {
		t.Fatalf("Execute() error = nil, want error")
	}
}

func TestRootCLI_SearchCommand_FailuresOnlyAsConstraint(t *testing.T) {
	t.Setenv("TRACEARY_REPO", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	initStub := &initializeStoreUsecaseStub{}
	searchStub := &searchEventsQueryServiceStub{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:   initStub,
		SearchEventsQueryService: searchStub,
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--failures",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; --failures alone should count as a valid search constraint", err)
	}
	if !searchStub.called {
		t.Fatalf("SearchEventsQueryService.Run() was not called")
	}
	if !searchStub.receivedInput.FailuresOnly {
		t.Fatalf("FailuresOnly = false, want true")
	}
}
