package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SessionTreeCommand_JSON(t *testing.T) {
	t.Parallel()

	t.Run("outputs nested JSON tree", func(t *testing.T) {
		t.Parallel()

		endedAt := time.Date(2026, 4, 9, 13, 30, 0, 0, time.UTC)
		initStub := &initializeStoreUsecaseStub{}
		listStub := &listSessionsQueryServiceStub{
			summaries: []*port.SessionSummary{
				{
					SessionID:   "root-session",
					Workspace:        "duck8823/traceary",
					Label:       "sprint",
					StartedAt:   time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					EndedAt:     &endedAt,
					Status:      "ended",
					TotalEvents: 10,
					CommandCount: 5,
					Agents:      []string{"claude"},
				},
				{
					SessionID:       "child-session",
					Workspace:            "duck8823/traceary",
					ParentSessionID: "root-session",
					StartedAt:       time.Date(2026, 4, 9, 12, 30, 0, 0, time.UTC),
					Status:          "active",
					TotalEvents:     3,
					CommandCount:    2,
					Agents:          []string{"codex"},
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
			"session", "tree",
			"--db-path", "/tmp/test-traceary.db",
			"--json",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()

		// Verify it's valid JSON
		var tree []json.RawMessage
		if err := json.Unmarshal([]byte(output), &tree); err != nil {
			t.Fatalf("output is not valid JSON array: %v\noutput: %s", err, output)
		}
		if len(tree) != 1 {
			t.Fatalf("expected 1 root node, got %d", len(tree))
		}

		// Verify root node fields
		if !strings.Contains(output, `"session_id": "root-session"`) {
			t.Fatalf("JSON output should contain root session_id, got: %s", output)
		}
		if !strings.Contains(output, `"status": "ended"`) {
			t.Fatalf("JSON output should contain status, got: %s", output)
		}
		if !strings.Contains(output, `"duration_sec"`) {
			t.Fatalf("JSON output should contain duration_sec for ended session, got: %s", output)
		}
		if !strings.Contains(output, `"label": "sprint"`) {
			t.Fatalf("JSON output should contain label, got: %s", output)
		}

		// Verify nested children
		if !strings.Contains(output, `"session_id": "child-session"`) {
			t.Fatalf("JSON output should contain child session_id, got: %s", output)
		}
		// Child has no ended_at so no duration_sec — just verify it's nested in children
		if !strings.Contains(output, `"children"`) {
			t.Fatalf("JSON output should contain children field, got: %s", output)
		}
	})

	t.Run("outputs empty JSON array when no sessions", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
			ListSessionsQueryService: &listSessionsQueryServiceStub{},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "tree", "--db-path", dbPath, "--json"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if strings.TrimSpace(stdout.String()) != "[]" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "[]\n")
		}
	})

	t.Run("text output is unchanged without --json", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		listStub := &listSessionsQueryServiceStub{
			summaries: []*port.SessionSummary{
				{
					SessionID:   "text-session",
					Workspace:        "duck8823/traceary",
					StartedAt:   time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					Status:      "active",
					TotalEvents: 1,
					CommandCount: 0,
					Agents:      []string{},
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
		rootCmd.SetArgs([]string{"session", "tree", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		output := stdout.String()
		if !strings.Contains(output, "text-session") {
			t.Fatalf("text output should contain session id, got: %s", output)
		}
		// Text output should NOT be JSON
		if strings.HasPrefix(strings.TrimSpace(output), "[") {
			t.Fatalf("text output should not be JSON, got: %s", output)
		}
	})
}
