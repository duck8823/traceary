package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/presentation/cli"
)

type listSessionsQueryServiceStub struct {
	receivedPath  string
	receivedInput port.ListSessionsInput
	called        bool
	summaries     []*port.SessionSummary
	err           error
}

func (s *listSessionsQueryServiceStub) Run(
	_ context.Context,
	dbPath string,
	input port.ListSessionsInput,
) ([]*port.SessionSummary, error) {
	s.called = true
	s.receivedPath = dbPath
	s.receivedInput = input
	return s.summaries, s.err
}

var _ queryservice.ListSessionsQueryService = (*listSessionsQueryServiceStub)(nil)

func TestRootCLI_SessionListCommand(t *testing.T) {
	t.Parallel()

	t.Run("displays session list", func(t *testing.T) {
		t.Parallel()

		endedAt := time.Date(2026, 4, 9, 13, 30, 0, 0, time.UTC)
		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		initStub := &initializeStoreUsecaseStub{}
		listStub := &listSessionsQueryServiceStub{
			summaries: []*port.SessionSummary{
				{
					SessionID:       "session-1",
					Repo:            "duck8823/traceary",
					Label:           "docs",
					Summary:         "Document the public session metadata surface for operators.",
					ParentSessionID: "parent-1",
					StartedAt:       time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					EndedAt:         &endedAt,
					Status:          "ended",
					TotalEvents:     42,
					CommandCount:    30,
					Agents:          []string{"claude", "codex"},
				},
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   initStub,
			ListSessionsQueryService: listStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"session", "list",
			"--db-path", dbPath,
			"--repo", "duck8823/traceary",
			"--agent", "claude",
			"--limit", "10",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !listStub.called {
			t.Fatalf("ListSessionsQueryService.Run() was not called")
		}
		output := stdout.String()
		if !strings.Contains(output, "session-1") {
			t.Fatalf("output should contain session-1, got: %s", output)
		}
		if !strings.Contains(output, "1h30m") {
			t.Fatalf("output should contain duration 1h30m, got: %s", output)
		}
		if !strings.Contains(output, "claude, codex") {
			t.Fatalf("output should contain agents, got: %s", output)
		}
		if !strings.Contains(output, "docs") {
			t.Fatalf("output should contain label, got: %s", output)
		}
		if !strings.Contains(output, "parent-1") {
			t.Fatalf("output should contain parent session id, got: %s", output)
		}
		if !strings.Contains(output, "Document the public session metadata surface for operators.") {
			t.Fatalf("output should contain summary, got: %s", output)
		}
	})

	t.Run("displays message when no sessions exist", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
			ListSessionsQueryService: &listSessionsQueryServiceStub{},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "No sessions found.\n" {
			t.Fatalf("stdout = %q, want empty message", stdout.String())
		}
	})

	t.Run("JSON 形式で出力できる", func(t *testing.T) {
		t.Parallel()

		endedAt := time.Date(2026, 4, 9, 12, 5, 0, 0, time.UTC)
		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		listStub := &listSessionsQueryServiceStub{
			summaries: []*port.SessionSummary{
				{
					SessionID:       "session-json",
					Label:           "release",
					Summary:         "Prepare release notes",
					ParentSessionID: "root-session",
					StartedAt:       time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					EndedAt:         &endedAt,
					Status:          "ended",
					TotalEvents:     5,
					CommandCount:    3,
					Agents:          []string{"claude"},
				},
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
			ListSessionsQueryService: listStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", dbPath, "--json"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		output := stdout.String()
		if !strings.Contains(output, `"session_id": "session-json"`) {
			t.Fatalf("JSON output should contain session_id, got: %s", output)
		}
		if !strings.Contains(output, `"duration_sec"`) {
			t.Fatalf("JSON output should contain duration_sec, got: %s", output)
		}
		if !strings.Contains(output, `"label": "release"`) {
			t.Fatalf("JSON output should contain label, got: %s", output)
		}
		if !strings.Contains(output, `"summary": "Prepare release notes"`) {
			t.Fatalf("JSON output should contain summary, got: %s", output)
		}
		if !strings.Contains(output, `"parent_session_id": "root-session"`) {
			t.Fatalf("JSON output should contain parent_session_id, got: %s", output)
		}
	})

	t.Run("text output sanitizes metadata columns", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		listStub := &listSessionsQueryServiceStub{
			summaries: []*port.SessionSummary{
				{
					SessionID:       "session-sanitized",
					Label:           "release\tcandidate",
					Summary:         "Keep summary output readable",
					ParentSessionID: "root\nsession",
					StartedAt:       time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					Status:          "active",
				},
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
			ListSessionsQueryService: listStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		output := stdout.String()
		if strings.Contains(output, "release\tcandidate") {
			t.Fatalf("text output should not contain raw tab characters in label, got: %q", output)
		}
		if strings.Contains(output, "root\nsession") {
			t.Fatalf("text output should not contain raw newlines in parent session id, got: %q", output)
		}
		if !strings.Contains(output, "release candidate") {
			t.Fatalf("text output should normalize label whitespace, got: %q", output)
		}
		if !strings.Contains(output, "root session") {
			t.Fatalf("text output should normalize parent session id whitespace, got: %q", output)
		}
	})

	t.Run("--from が不正な形式ならエラー", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
			ListSessionsQueryService: &listSessionsQueryServiceStub{},
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", dbPath, "--from", "invalid"})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
	})

	t.Run("--to が不正な形式ならエラー", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
			ListSessionsQueryService: &listSessionsQueryServiceStub{},
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", dbPath, "--to", "not-a-date"})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
	})

	t.Run("--client filter is passed to query service", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		listStub := &listSessionsQueryServiceStub{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
			ListSessionsQueryService: listStub,
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", dbPath, "--client", "hook"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !listStub.called {
			t.Fatal("ListSessionsQueryService.Run() was not called")
		}
		if listStub.receivedInput.Client != "hook" {
			t.Fatalf("Client = %q, want %q", listStub.receivedInput.Client, "hook")
		}
	})
}
