package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:       initStub,
		RecordSessionBoundaryUsecase: sessionStub,
	}).Command()
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

func TestRootCLI_SessionStartCommand_IdOnly(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDOf("event-start-id-only")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-start-id-only")
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
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:       initStub,
		RecordSessionBoundaryUsecase: sessionStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "start", "--db-path", dbPath, "--id-only"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() != "session-start-id-only\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "session-start-id-only\n")
	}
}

func TestRootCLI_SessionStartCommand_JSON(t *testing.T) {
	t.Parallel()

	eventID := mustEventID(t, "event-start-json")
	agent := mustAgent(t, "codex")
	sessionID := mustSessionID(t, "session-start-json")

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
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:       initStub,
		RecordSessionBoundaryUsecase: sessionStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "start", "--db-path", dbPath, "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	payload := decodeJSONMap(t, stdout.String())
	if got, want := payload["event_id"], "event-start-json"; got != want {
		t.Fatalf("event_id = %v, want %q", got, want)
	}
	if got, want := payload["session_id"], "session-start-json"; got != want {
		t.Fatalf("session_id = %v, want %q", got, want)
	}
}

func TestRootCLI_SessionStartCommand_UsesDetectedRepoByDefault(t *testing.T) {
	t.Setenv("TRACEARY_REPO", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDOf("event-1b")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-auto-repo")
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
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 7, 13, 5, 0, 0, time.UTC),
		),
	}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:       initStub,
		RecordSessionBoundaryUsecase: sessionStub,
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "start", "--db-path", dbPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if sessionStub.receivedInput.Repo != "github.com/duck8823/traceary" {
		t.Fatalf("Repo = %q, want %q", sessionStub.receivedInput.Repo, "github.com/duck8823/traceary")
	}
}

func TestRootCLI_SessionEndCommand(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")
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
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:       initStub,
		RecordSessionBoundaryUsecase: sessionStub,
	}).Command()
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
	if sessionStub.receivedInput.Client != "" {
		t.Fatalf("Client = %q, want empty", sessionStub.receivedInput.Client)
	}
	if sessionStub.receivedInput.Agent != "" {
		t.Fatalf("Agent = %q, want empty", sessionStub.receivedInput.Agent)
	}
	if sessionStub.receivedInput.DefaultClient != "cli" {
		t.Fatalf("DefaultClient = %q, want %q", sessionStub.receivedInput.DefaultClient, "cli")
	}
	if sessionStub.receivedInput.DefaultAgent != "manual" {
		t.Fatalf("DefaultAgent = %q, want %q", sessionStub.receivedInput.DefaultAgent, "manual")
	}
	if sessionStub.receivedInput.Repo != "" {
		t.Fatalf("Repo = %q, want empty", sessionStub.receivedInput.Repo)
	}
	if sessionStub.receivedInput.DefaultRepo != "github.com/duck8823/traceary" {
		t.Fatalf("DefaultRepo = %q, want %q", sessionStub.receivedInput.DefaultRepo, "github.com/duck8823/traceary")
	}
	if stdout.String() != "Recorded: event-2\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "Recorded: event-2\n")
	}
}

func TestRootCLI_SessionEndCommand_IdOnly(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDOf("event-end-id-only")
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
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:       initStub,
		RecordSessionBoundaryUsecase: sessionStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "end", "--db-path", dbPath, "--id-only", "--session-id", "session-env"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() != "event-end-id-only\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "event-end-id-only\n")
	}
}

func TestRootCLI_SessionEndCommand_JSON(t *testing.T) {
	t.Parallel()

	eventID := mustEventID(t, "event-end-json")
	agent := mustAgent(t, "codex")
	sessionID := mustSessionID(t, "session-end-json")

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	initStub := &initializeStoreUsecaseStub{}
	sessionStub := &recordSessionBoundaryUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindSessionEnded,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"session ended",
			time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:       initStub,
		RecordSessionBoundaryUsecase: sessionStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "end", "--db-path", dbPath, "--session-id", "session-end-json", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	payload := decodeJSONMap(t, stdout.String())
	if got, want := payload["event_id"], "event-end-json"; got != want {
		t.Fatalf("event_id = %v, want %q", got, want)
	}
	if got, want := payload["kind"], "session_ended"; got != want {
		t.Fatalf("kind = %v, want %q", got, want)
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
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:        initStub,
		FindLatestSessionQueryService: latestStub,
	}).Command()
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

func TestRootCLI_SessionLatestCommand_JSON(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	initStub := &initializeStoreUsecaseStub{}
	latestStub := &findLatestSessionQueryServiceStub{
		event: model.EventOf(
			mustEventID(t, "event-latest-json"),
			types.EventKindSessionStarted,
			"cli",
			mustAgent(t, "codex"),
			mustSessionID(t, "session-latest-json"),
			"duck8823/traceary",
			"session started",
			time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:        initStub,
		FindLatestSessionQueryService: latestStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "latest", "--db-path", dbPath, "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	payload := decodeJSONMap(t, stdout.String())
	if got, want := payload["event_id"], "event-latest-json"; got != want {
		t.Fatalf("event_id = %v, want %q", got, want)
	}
	if got, want := payload["session_id"], "session-latest-json"; got != want {
		t.Fatalf("session_id = %v, want %q", got, want)
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
			time.Now().Add(-1*time.Hour),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:        initStub,
		FindLatestSessionQueryService: latestStub,
	}).Command()
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

func TestRootCLI_SessionActiveCommand_StaleError(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDOf("event-5")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-stale")
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
			time.Now().Add(-48*time.Hour),
		),
	}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:        initStub,
		FindLatestSessionQueryService: latestStub,
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"active",
		"--db-path", dbPath,
	})

	err = rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %q, want stale error", err.Error())
	}
}

func TestRootCLI_SessionActiveCommand_AllowStale(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDOf("event-6")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-stale")
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
			time.Now().Add(-48*time.Hour),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:        initStub,
		FindLatestSessionQueryService: latestStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"active",
		"--db-path", dbPath,
		"--allow-stale",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() != "session-stale\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "session-stale\n")
	}
}

func TestRootCLI_SessionLatestCommand_NotFoundError(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	initStub := &initializeStoreUsecaseStub{}
	latestStub := &findLatestSessionQueryServiceStub{
		err: queryservice.ErrSessionNotFound,
	}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:        initStub,
		FindLatestSessionQueryService: latestStub,
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "latest", "--db-path", dbPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if err.Error() != "no matching session found" {
		t.Fatalf("error = %q, want %q", err.Error(), "no matching session found")
	}
}

func decodeJSONMap(t *testing.T, value string) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return payload
}

func mustAgent(t *testing.T, value string) types.Agent {
	t.Helper()

	agent, err := types.AgentOf(value)
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}

	return agent
}
