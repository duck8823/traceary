package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type searchEventsQueryServiceStub struct {
	receivedPath  string
	receivedInput queryservice.SearchEventsInput
	called        bool
	events        []*model.Event
	err           error
}

func (s *searchEventsQueryServiceStub) Run(
	_ context.Context,
	dbPath string,
	input queryservice.SearchEventsInput,
) ([]*model.Event, error) {
	s.called = true
	s.receivedPath = dbPath
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
	rootCmd := cli.NewRootCLI(initStub, nil, nil, nil, nil, searchStub, nil).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--from", "2026-04-07",
		"--to", "2026-04-07",
		"--limit", "5",
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
	if stdout.String() == "" {
		t.Fatalf("stdout is empty")
	}
}
