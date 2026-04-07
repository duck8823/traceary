package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type recordSessionBoundaryUsecaseStub struct {
	receivedInput usecase.RecordSessionBoundaryInput
	called        bool
	event         *model.Event
	err           error
}

func (s *recordSessionBoundaryUsecaseStub) Run(
	_ context.Context,
	input usecase.RecordSessionBoundaryInput,
) (*model.Event, error) {
	s.called = true
	s.receivedInput = input
	return s.event, s.err
}

var _ usecase.RecordSessionBoundaryUsecase = (*recordSessionBoundaryUsecaseStub)(nil)

type findLatestSessionQueryServiceStub struct {
	receivedPath  string
	receivedInput queryservice.FindLatestSessionInput
	called        bool
	event         *model.Event
	err           error
}

func (s *findLatestSessionQueryServiceStub) Run(
	_ context.Context,
	dbPath string,
	input queryservice.FindLatestSessionInput,
) (*model.Event, error) {
	s.called = true
	s.receivedPath = dbPath
	s.receivedInput = input
	return s.event, s.err
}

var _ queryservice.FindLatestSessionQueryService = (*findLatestSessionQueryServiceStub)(nil)

func TestRootCLI_SessionStartCommand(t *testing.T) {
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
	sessionStub := &recordSessionBoundaryUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindSessionStarted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"session started",
			time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(initStub, nil, sessionStub, nil, nil, nil, nil, nil, nil, nil).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"start",
		"--db-path", dbPath,
		"--client", "cli",
		"--agent", "codex",
		"--repo", "duck8823/traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !sessionStub.called {
		t.Fatalf("RecordSessionBoundaryUsecase.Run() was not called")
	}
	if sessionStub.receivedInput.Kind != types.EventKindSessionStarted {
		t.Fatalf("Kind = %q, want %q", sessionStub.receivedInput.Kind, types.EventKindSessionStarted)
	}
	if stdout.String() != "session-1\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "session-1\n")
	}
}

func TestRootCLI_SessionEndCommand(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDOf("event-2")
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

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	initStub := &initializeStoreUsecaseStub{}
	sessionStub := &recordSessionBoundaryUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindSessionEnded,
			"cli",
			agent,
			sessionID,
			"",
			"session ended",
			time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(initStub, nil, sessionStub, nil, nil, nil, nil, nil, nil, nil).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "end", "--db-path", dbPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if sessionStub.receivedInput.Kind != types.EventKindSessionEnded {
		t.Fatalf("Kind = %q, want %q", sessionStub.receivedInput.Kind, types.EventKindSessionEnded)
	}
	if sessionStub.receivedInput.SessionID != "session-env" {
		t.Fatalf("SessionID = %q, want %q", sessionStub.receivedInput.SessionID, "session-env")
	}
	if stdout.String() != "session-env\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "session-env\n")
	}
}

func TestRootCLI_SessionLatestCommand(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDOf("event-3")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-latest")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	initStub := &initializeStoreUsecaseStub{}
	latestStub := &findLatestSessionQueryServiceStub{
		event: model.EventOf(
			eventID,
			types.EventKindSessionStarted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"session started",
			time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(initStub, nil, nil, nil, nil, nil, nil, nil, latestStub, nil).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"latest",
		"--db-path", dbPath,
		"--client", "cli",
		"--agent", "codex",
		"--repo", "duck8823/traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !initStub.called {
		t.Fatalf("InitializeStoreUsecase.Run() was not called")
	}
	if !latestStub.called {
		t.Fatalf("FindLatestSessionQueryService.Run() was not called")
	}
	if latestStub.receivedPath != dbPath {
		t.Fatalf("dbPath = %q, want %q", latestStub.receivedPath, dbPath)
	}
	if latestStub.receivedInput.Agent != "codex" {
		t.Fatalf("Agent = %q, want %q", latestStub.receivedInput.Agent, "codex")
	}
	if latestStub.receivedInput.ActiveOnly {
		t.Fatalf("ActiveOnly = %t, want false", latestStub.receivedInput.ActiveOnly)
	}
	if stdout.String() != "session-latest\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "session-latest\n")
	}
}

func TestRootCLI_SessionActiveCommand(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDOf("event-4")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-active")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	initStub := &initializeStoreUsecaseStub{}
	latestStub := &findLatestSessionQueryServiceStub{
		event: model.EventOf(
			eventID,
			types.EventKindSessionStarted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"session started",
			time.Date(2026, 4, 8, 14, 0, 0, 0, time.UTC),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(initStub, nil, nil, nil, nil, nil, nil, nil, latestStub, nil).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"active",
		"--db-path", dbPath,
		"--agent", "codex",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !latestStub.called {
		t.Fatalf("FindLatestSessionQueryService.Run() was not called")
	}
	if !latestStub.receivedInput.ActiveOnly {
		t.Fatalf("ActiveOnly = %t, want true", latestStub.receivedInput.ActiveOnly)
	}
	if stdout.String() != "session-active\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "session-active\n")
	}
}
