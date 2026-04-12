package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SessionListCommand(t *testing.T) {
	t.Parallel()

	t.Run("displays session list", func(t *testing.T) {
		t.Parallel()

		endedAt := time.Date(2026, 4, 9, 13, 30, 0, 0, time.UTC)
		listStub := &sessionUsecaseStub{
			listResult: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("session-1"),
					types.Workspace("duck8823/traceary"),
					time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					types.Of(endedAt),
					"ended",
					42,
					30,
					[]string{"claude", "codex"},
					"docs",
					"Document the public session metadata surface for operators.",
					types.SessionID("parent-1"),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Session:          listStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"session", "list",
			"--db-path",
			"/tmp/test-traceary.db",
			"--workspace", "duck8823/traceary",
			"--agent", "claude",
			"--limit", "10",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
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
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Session:          &sessionUsecaseStub{},
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
		listStub := &sessionUsecaseStub{
			listResult: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("session-json"),
					types.Workspace(""),
					time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					types.Of(endedAt),
					"ended",
					5,
					3,
					[]string{"claude"},
					"release",
					"Prepare release notes",
					types.SessionID("root-session"),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Session:          listStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", "/tmp/test-traceary.db", "--json"})

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
		listStub := &sessionUsecaseStub{
			listResult: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("session-sanitized"),
					types.Workspace(""),
					time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					types.Empty[time.Time](),
					"active",
					0,
					0,
					nil,
					"release\tcandidate",
					"Keep summary output readable",
					types.SessionID("root\nsession"),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Session:          listStub,
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

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Session:          &sessionUsecaseStub{},
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", "/tmp/test-traceary.db", "--from", "invalid"})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
	})

	t.Run("--to が不正な形式ならエラー", func(t *testing.T) {
		t.Parallel()

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Session:          &sessionUsecaseStub{},
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", "/tmp/test-traceary.db", "--to", "not-a-date"})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
	})

	t.Run("--client filter is passed to query service", func(t *testing.T) {
		t.Parallel()

		listStub := &sessionUsecaseStub{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Session:          listStub,
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "list", "--db-path", "/tmp/test-traceary.db", "--client", "hook"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	})
}
