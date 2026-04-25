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

func TestRootCLI_SessionTreeCommand_LineageFields(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	ended := started.Add(90 * time.Second)
	listStub := &sessionUsecaseStub{
		listResult: []apptypes.SessionSummary{
			apptypes.SessionSummaryOf(
				types.SessionID("parent-session"),
				types.Workspace("duck8823/traceary"),
				started,
				types.Some(ended),
				"ended",
				12,
				8,
				[]string{"claude"},
				"",
				"",
				types.SessionID(""),
			),
			apptypes.SessionSummaryOf(
				types.SessionID("child-session"),
				types.Workspace("duck8823/traceary"),
				started.Add(5*time.Second),
				types.Some(ended),
				"ended",
				4,
				3,
				[]string{"claude/explore"},
				"",
				"",
				types.SessionID("parent-session"),
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
	rootCmd.SetArgs([]string{"session", "tree", "--db-path", "/tmp/test-traceary.db", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var trees []struct {
		SessionID       string   `json:"session_id"`
		ParentSessionID string   `json:"parent_session_id"`
		Depth           int      `json:"depth"`
		DurationSec     *float64 `json:"duration_sec"`
		SubagentType    string   `json:"subagent_type"`
		Children        []struct {
			SessionID       string `json:"session_id"`
			ParentSessionID string `json:"parent_session_id"`
			Depth           int    `json:"depth"`
			SubagentType    string `json:"subagent_type"`
		} `json:"children"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &trees); err != nil {
		t.Fatalf("json.Unmarshal: %v (body=%s)", err, stdout.String())
	}
	if len(trees) != 1 || trees[0].SessionID != "parent-session" {
		t.Fatalf("unexpected tree shape: %+v", trees)
	}
	root := trees[0]
	if root.Depth != 0 {
		t.Fatalf("root depth = %d, want 0", root.Depth)
	}
	if root.ParentSessionID != "" {
		t.Fatalf("root parent_session_id = %q, want empty", root.ParentSessionID)
	}
	if root.DurationSec == nil || *root.DurationSec != 90 {
		t.Fatalf("root duration_sec = %v, want 90", root.DurationSec)
	}
	if strings.Contains(stdout.String(), `"duration_ms"`) {
		t.Fatalf("JSON output should not contain duration_ms, got: %s", stdout.String())
	}
	if root.SubagentType != "claude" {
		t.Fatalf("root subagent_type = %q, want claude", root.SubagentType)
	}
	if len(root.Children) != 1 || root.Children[0].SessionID != "child-session" {
		t.Fatalf("unexpected children: %+v", root.Children)
	}
	child := root.Children[0]
	if child.Depth != 1 || child.ParentSessionID != "parent-session" || child.SubagentType != "claude/explore" {
		t.Fatalf("unexpected child: %+v", child)
	}
}

func TestRootCLI_SessionTreeCommand_RootFilter(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	ended := started.Add(time.Minute)
	listStub := &sessionUsecaseStub{
		listResult: []apptypes.SessionSummary{
			apptypes.SessionSummaryOf(
				types.SessionID("root-a"),
				types.Workspace("ws"),
				started, types.Some(ended),
				"ended", 3, 2, []string{"claude"}, "", "", types.SessionID(""),
			),
			apptypes.SessionSummaryOf(
				types.SessionID("root-b"),
				types.Workspace("ws"),
				started, types.Some(ended),
				"ended", 3, 2, []string{"codex"}, "", "", types.SessionID(""),
			),
			apptypes.SessionSummaryOf(
				types.SessionID("child-of-b"),
				types.Workspace("ws"),
				started, types.Some(ended),
				"ended", 1, 1, []string{"codex/explore"}, "", "", types.SessionID("root-b"),
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
	rootCmd.SetArgs([]string{"session", "tree", "--db-path", "/tmp/test-traceary.db", "--root", "root-b"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "root-b") {
		t.Fatalf("--root root-b should keep root-b, got %q", out)
	}
	if strings.Contains(out, "root-a") {
		t.Fatalf("--root root-b should hide root-a, got %q", out)
	}
	if !strings.Contains(out, "child-of-b") {
		t.Fatalf("--root root-b should keep its descendant, got %q", out)
	}
}

func TestRootCLI_SessionTreeCommand_OngoingOnly(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	ended := started.Add(time.Minute)
	listStub := &sessionUsecaseStub{
		listResult: []apptypes.SessionSummary{
			apptypes.SessionSummaryOf(
				types.SessionID("live-root"),
				types.Workspace("ws"),
				started, types.None[time.Time](),
				"active", 1, 0, []string{"claude"}, "", "", types.SessionID(""),
			),
			apptypes.SessionSummaryOf(
				types.SessionID("dead-root"),
				types.Workspace("ws"),
				started, types.Some(ended),
				"ended", 3, 2, []string{"codex"}, "", "", types.SessionID(""),
			),
			apptypes.SessionSummaryOf(
				types.SessionID("dead-child"),
				types.Workspace("ws"),
				started, types.Some(ended),
				"ended", 1, 1, []string{"codex"}, "", "", types.SessionID("dead-root"),
			),
			// Stale (status=stale, no end event) sessions must not leak into
			// --ongoing-only output — once the datasource has promoted them
			// to stale, they are no longer the live work the flag promises.
			apptypes.SessionSummaryOf(
				types.SessionID("stale-root"),
				types.Workspace("ws"),
				started, types.None[time.Time](),
				"stale", 1, 0, []string{"claude"}, "", "", types.SessionID(""),
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
	rootCmd.SetArgs([]string{"session", "tree", "--db-path", "/tmp/test-traceary.db", "--ongoing-only"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "live-root") {
		t.Fatalf("--ongoing-only should keep live-root, got %q", out)
	}
	if strings.Contains(out, "dead-root") || strings.Contains(out, "dead-child") {
		t.Fatalf("--ongoing-only should prune dead lineage, got %q", out)
	}
	if strings.Contains(out, "stale-root") {
		t.Fatalf("--ongoing-only should prune stale sessions, got %q", out)
	}
}
