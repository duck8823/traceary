package cli_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
	"github.com/google/go-cmp/cmp"
)

func TestRootCLI_DeprecationNotice(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		json       bool
		wantNotice string
	}{
		{
			name:       "top snapshot",
			command:    "top",
			wantNotice: "DEPRECATED: this command is deprecated, use `traceary sessions` instead. Removal target: v0.35.\n",
		},
		{
			name:       "top JSON snapshot",
			command:    "top",
			json:       true,
			wantNotice: "DEPRECATED: this command is deprecated, use `traceary sessions` instead. Removal target: v0.35.\n",
		},
		{
			name:    "sessions snapshot",
			command: "sessions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithSession(&sessionUsecaseStub{}),
				cli.WithEvent(&topPaneEventStub{}),
				cli.WithMemory(&memoryUsecaseStub{}),
			).Command()
			args := []string{tt.command, "--db-path", "/tmp/traceary-deprecation-test.db", "--snapshot"}
			if tt.json {
				args = append(args, "--json")
			}
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(args)
			if err := root.Execute(); err != nil && tt.wantNotice == "" {
				t.Fatalf("Execute() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantNotice, stderr.String()); diff != "" {
				t.Errorf("stderr notice mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(strings.Count(tt.wantNotice, "\n"), strings.Count(stderr.String(), "\n")); diff != "" {
				t.Errorf("stderr line count mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRootCLI_NoReplacementDeprecationNotice(t *testing.T) {
	edge := model.MemoryEdgeOf(
		types.MemoryEdgeID("edge-1"),
		types.MemoryID("memory-a"),
		types.MemoryID("memory-b"),
		types.MemoryEdgeRelation("supports"),
		time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		types.None[time.Time](),
		time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
	)
	tests := []struct {
		name        string
		language    string
		args        []string
		wantNotice  string
		wantStdout  string
		memoryEdges bool
		withSession bool
	}{
		{name: "graph add", args: []string{"memory", "admin", "graph", "add", "memory-a", "--to", "memory-b", "--relation", "supports", "--db-path", "/tmp/traceary-deprecation-graph-add.db"}, wantNotice: "DEPRECATED: this command is deprecated with no replacement. Removal target: v0.35.\n", wantStdout: "edge-1\tmemory-a\tsupports\tmemory-b\tvalid_from=2026-08-10T00:00:00Z\tvalid_to=-\n", memoryEdges: true},
		{name: "graph list", args: []string{"memory", "admin", "graph", "list", "--db-path", "/tmp/traceary-deprecation-graph-list.db"}, wantNotice: "DEPRECATED: this command is deprecated with no replacement. Removal target: v0.35.\n", wantStdout: "- No matching edges.\n", memoryEdges: true},
		{name: "graph parent", args: []string{"memory", "admin", "graph"}, memoryEdges: true},
		{name: "session label", args: []string{"session", "label", "example", "--session-id", "session-1", "--db-path", "/tmp/traceary-deprecation-session-label.db"}, wantNotice: "DEPRECATED: this command is deprecated with no replacement. Removal target: v0.35.\n", wantStdout: "Label set: session-1 -> example\n", withSession: true},
		{name: "session list with label", args: []string{"session", "list", "--label", "foo", "--db-path", "/tmp/traceary-deprecation-session-list-label.db"}, wantNotice: "DEPRECATED: the `--label` flag is deprecated with no replacement. Removal target: v0.35.\n", wantStdout: "No sessions found.\n", withSession: true},
		{name: "session list without label", args: []string{"session", "list", "--db-path", "/tmp/traceary-deprecation-session-list.db"}, wantStdout: "No sessions found.\n", withSession: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.language != "" {
				t.Setenv("TRACEARY_LANG", tt.language)
			}
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			opts := []cli.RootCLIOption{cli.WithStoreManagement(&storeManagementUsecaseStub{})}
			if tt.memoryEdges {
				opts = append(opts, cli.WithMemoryEdge(&memoryEdgeUsecaseStub{addEdge: edge}))
			}
			if tt.withSession {
				opts = append(opts, cli.WithSession(&sessionUsecaseStub{}))
			}
			root := cli.NewRootCLI(opts...).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantNotice, stderr.String()); diff != "" {
				t.Errorf("stderr notice mismatch (-want +got):\n%s", diff)
			}
			if tt.name != "graph parent" {
				if diff := cmp.Diff(tt.wantStdout, stdout.String()); diff != "" {
					t.Errorf("stdout mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// Cobra validates required flags after PreRunE but before the run step, so a
// notice attached to PreRunE would fire for an invocation that never runs.
func TestRootCLI_DeprecationNoticeSkipsRequiredFlagFailures(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "graph add without --to", args: []string{"memory", "admin", "graph", "add", "memory-a", "--relation", "supports"}},
		{name: "graph add without --relation", args: []string{"memory", "admin", "graph", "add", "memory-a", "--to", "memory-b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemoryEdge(&memoryEdgeUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)

			if err := root.Execute(); err == nil {
				t.Fatalf("Execute() error = nil, want a required-flag error")
			}
			if diff := cmp.Diff("", stderr.String()); diff != "" {
				t.Errorf("unexpected notice for an invocation that never ran (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("", stdout.String()); diff != "" {
				t.Errorf("stdout mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRootCLI_NoReplacementDeprecationNoticeJapanese(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "ja")
	tests := []struct {
		name        string
		args        []string
		wantNotice  string
		wantStdout  string
		withGraph   bool
		withSession bool
	}{
		{name: "graph list", args: []string{"memory", "admin", "graph", "list", "--db-path", "/tmp/traceary-deprecation-ja-graph.db"}, wantNotice: "DEPRECATED: このコマンドは非推奨です。置き換え先はありません。削除予定: v0.35。\n", wantStdout: "- 一致する edge はありません\n", withGraph: true},
		{name: "session label", args: []string{"session", "label", "example", "--session-id", "session-1", "--db-path", "/tmp/traceary-deprecation-ja-label.db"}, wantNotice: "DEPRECATED: このコマンドは非推奨です。置き換え先はありません。削除予定: v0.35。\n", wantStdout: "ラベルを設定しました: session-1 -> example\n", withSession: true},
		{name: "session list with label", args: []string{"session", "list", "--label", "foo", "--db-path", "/tmp/traceary-deprecation-ja-list.db"}, wantNotice: "DEPRECATED: `--label` フラグは非推奨です。置き換え先はありません。削除予定: v0.35。\n", wantStdout: "セッションが見つかりません\n", withSession: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			opts := []cli.RootCLIOption{cli.WithStoreManagement(&storeManagementUsecaseStub{})}
			if tt.withGraph {
				opts = append(opts, cli.WithMemoryEdge(&memoryEdgeUsecaseStub{}))
			}
			if tt.withSession {
				opts = append(opts, cli.WithSession(&sessionUsecaseStub{}))
			}
			root := cli.NewRootCLI(opts...).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantNotice, stderr.String()); diff != "" {
				t.Errorf("stderr notice mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantStdout, stdout.String()); diff != "" {
				t.Errorf("stdout mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Cobra resolves --help and rejects invalid arguments before any command hook,
// so neither path can carry the notice. docs/cli-stability.md requires the help
// text itself to name the deprecation in exchange; both halves are pinned here.
func TestRootCLI_DeprecationNoticeSkipsNonRunningPaths(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantErr       bool
		wantStdoutHas string
	}{
		{
			name:          "help resolves before the hook and carries the deprecation itself",
			args:          []string{"top", "--help"},
			wantStdoutHas: "removed in v0.35",
		},
		{
			name:    "invalid arguments are rejected before the hook",
			args:    []string{"top", "unexpected"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithSession(&sessionUsecaseStub{}),
				cli.WithEvent(&topPaneEventStub{}),
				cli.WithMemory(&memoryUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)

			err := root.Execute()
			if diff := cmp.Diff(tt.wantErr, err != nil); diff != "" {
				t.Errorf("error presence mismatch (-want +got):\n%s (err = %v)", diff, err)
			}
			if strings.Contains(stderr.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice on stderr:\n%s", stderr.String())
			}
			if tt.wantStdoutHas != "" && !strings.Contains(stdout.String(), tt.wantStdoutHas) {
				t.Errorf("stdout does not mention the deprecation, want substring %q, got:\n%s", tt.wantStdoutHas, stdout.String())
			}
		})
	}
}

func TestRootCLI_TopSnapshotOutputMatchesSessions(t *testing.T) {
	tests := []struct {
		name string
		json bool
	}{
		{name: "text snapshot"},
		{name: "JSON snapshot", json: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := func(command string) string {
				stdout := &bytes.Buffer{}
				root := cli.NewRootCLI(
					cli.WithStoreManagement(&storeManagementUsecaseStub{}),
					cli.WithSession(&sessionUsecaseStub{}),
					cli.WithEvent(&topPaneEventStub{}),
					cli.WithMemory(&memoryUsecaseStub{}),
				).Command()
				args := []string{command, "--db-path", "/tmp/traceary-deprecation-test.db", "--snapshot"}
				if tt.json {
					args = append(args, "--json")
				}
				root.SetOut(stdout)
				root.SetErr(&bytes.Buffer{})
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("Execute(%s) error = %v", command, err)
				}
				return stdout.String()
			}

			if diff := cmp.Diff(run("sessions"), run("top")); diff != "" {
				t.Errorf("stdout differs (-sessions +top):\n%s", diff)
			}
		})
	}
}

func TestRootCLI_SessionsNonTTYMatchesSnapshot(t *testing.T) {
	run := func(args ...string) (string, string, error) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		root := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithSession(&sessionUsecaseStub{}),
			cli.WithEvent(&topPaneEventStub{}),
			cli.WithMemory(&memoryUsecaseStub{}),
		).Command()
		root.SetOut(stdout)
		root.SetErr(stderr)
		root.SetArgs(args)
		err := root.Execute()
		return stdout.String(), stderr.String(), err
	}

	bareStdout, bareStderr, err := run("sessions", "--db-path", "/tmp/traceary-sessions-nontty.db")
	if err != nil {
		t.Fatalf("bare sessions error = %v", err)
	}
	snapshotStdout, snapshotStderr, err := run("sessions", "--db-path", "/tmp/traceary-sessions-nontty.db", "--snapshot")
	if err != nil {
		t.Fatalf("sessions --snapshot error = %v", err)
	}
	if diff := cmp.Diff(snapshotStdout, bareStdout); diff != "" {
		t.Errorf("non-TTY bare sessions stdout differs from snapshot (-snapshot +bare):\n%s", diff)
	}
	if diff := cmp.Diff("", bareStderr+snapshotStderr); diff != "" {
		t.Errorf("non-TTY sessions emitted stderr (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionsValidationRequiresSnapshot(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "json", args: []string{"sessions", "--json"}, want: "--json requires --snapshot"},
		{name: "ai profile", args: []string{"sessions", "--profile", "ai"}, want: "--profile ai requires --snapshot --json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithSession(&sessionUsecaseStub{}),
				cli.WithEvent(&topPaneEventStub{}),
				cli.WithMemory(&memoryUsecaseStub{}),
			).Command()
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute() error = %v, want %q", err, tt.want)
			}
		})
	}
}
