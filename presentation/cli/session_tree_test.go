package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SessionTreeCommand_JSON(t *testing.T) {
	t.Parallel()

	t.Run("outputs nested JSON tree", func(t *testing.T) {
		t.Parallel()

		endedAt := time.Date(2026, 4, 9, 13, 30, 0, 0, time.UTC)
		listStub := &sessionUsecaseStub{
			listResult: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("root-session"),
					types.Workspace("duck8823/traceary"),
					time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					types.Some(endedAt),
					"ended",
					10,
					5,
					[]string{"claude"},
					"sprint",
					"",
					types.SessionID(""),
				),
				apptypes.SessionSummaryOf(
					types.SessionID("child-session"),
					types.Workspace("duck8823/traceary"),
					time.Date(2026, 4, 9, 12, 30, 0, 0, time.UTC),
					types.None[time.Time](),
					"active",
					3,
					2,
					[]string{"codex"},
					"",
					"",
					types.SessionID("root-session"),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithSession(listStub),
		).Command()
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
		// Child has no ended_at so no duration_sec -- just verify it's nested in children
		if !strings.Contains(output, `"children"`) {
			t.Fatalf("JSON output should contain children field, got: %s", output)
		}
	})

	t.Run("outputs empty JSON array when no sessions", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithSession(&sessionUsecaseStub{}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "tree", "--db-path", dbPath, "--json"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if diff := cmp.Diff("[]", strings.TrimSpace(stdout.String())); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("text output is unchanged without --json", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		listStub := &sessionUsecaseStub{
			listResult: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("text-session"),
					types.Workspace("duck8823/traceary"),
					time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					types.None[time.Time](),
					"active",
					1,
					0,
					[]string{},
					"",
					"",
					types.SessionID(""),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithSession(listStub),
		).Command()
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
