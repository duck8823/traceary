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

type getEventDetailsQueryServiceStub struct {
	receivedPath    string
	receivedEventID string
	called          bool
	eventDetails    *queryservice.EventDetails
	err             error
}

func (s *getEventDetailsQueryServiceStub) Run(
	_ context.Context,
	dbPath string,
	eventID string,
) (*queryservice.EventDetails, error) {
	s.called = true
	s.receivedPath = dbPath
	s.receivedEventID = eventID
	return s.eventDetails, s.err
}

var _ queryservice.GetEventDetailsQueryService = (*getEventDetailsQueryServiceStub)(nil)

func TestRootCLI_ShowCommand(t *testing.T) {
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

	t.Run("イベント詳細を表示できる", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		initStub := &initializeStoreUsecaseStub{}
		eventDetails, err := queryservice.NewEventDetails(
			model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
			),
			model.CommandAuditOf(
				eventID,
				"go test ./...",
				"stdin",
				"stdout",
				true,
				false,
			),
		)
		if err != nil {
			t.Fatalf("NewEventDetails() error = %v", err)
		}
		showStub := &getEventDetailsQueryServiceStub{eventDetails: eventDetails}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(initStub, nil, nil, nil, nil, nil, nil, showStub, nil, nil).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"show", "--db-path", dbPath, "event-1"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !initStub.called {
			t.Fatalf("InitializeStoreUsecase.Run() was not called")
		}
		if !showStub.called {
			t.Fatalf("GetEventDetailsQueryService.Run() was not called")
		}
		if showStub.receivedPath != dbPath {
			t.Fatalf("dbPath = %q, want %q", showStub.receivedPath, dbPath)
		}
		if showStub.receivedEventID != "event-1" {
			t.Fatalf("eventID = %q, want %q", showStub.receivedEventID, "event-1")
		}

		want := "" +
			"EVENT_ID: event-1\n" +
			"KIND: command_executed\n" +
			"CLIENT: cli\n" +
			"AGENT: codex\n" +
			"SESSION_ID: session-1\n" +
			"REPO: duck8823/traceary\n" +
			"CREATED_AT: 2026-04-08T12:00:00Z\n" +
			"MESSAGE: go test ./...\n" +
			"\n" +
			"COMMAND: go test ./...\n" +
			"INPUT_TRUNCATED: true\n" +
			"INPUT:\n" +
			"stdin\n" +
			"OUTPUT_TRUNCATED: false\n" +
			"OUTPUT:\n" +
			"stdout\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("command audit がないイベントも表示できる", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		initStub := &initializeStoreUsecaseStub{}
		eventDetails, err := queryservice.NewEventDetails(
			model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				sessionID,
				"",
				"hello",
				time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
			),
			nil,
		)
		if err != nil {
			t.Fatalf("NewEventDetails() error = %v", err)
		}
		showStub := &getEventDetailsQueryServiceStub{eventDetails: eventDetails}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(initStub, nil, nil, nil, nil, nil, nil, showStub, nil, nil).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"show", "--db-path", dbPath, "event-1"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if bytes.Contains(stdout.Bytes(), []byte("COMMAND:")) {
			t.Fatalf("stdout contains command audit section: %q", stdout.String())
		}
	})
}
