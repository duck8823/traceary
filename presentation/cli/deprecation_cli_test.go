package cli_test

import (
	"bytes"
	"strings"
	"testing"

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

// Removed v0.34 no-replacement surfaces must fail as unknown commands/flags
// without emitting a DEPRECATED notice (#1691). Assert the exact shipped
// wording so a leftover registered command that fails for another reason
// (for example a missing MemoryEdge dependency) cannot pass the test.
func TestRootCLI_RemovedV034NoReplacementSurfaces(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		// Drive the leaf path without extra flags so cobra reports the
		// unknown subcommand rather than an incidental unknown flag.
		{name: "graph add", args: []string{"memory", "admin", "graph", "add"}, wantErrHas: `unknown subcommand "graph"`},
		{name: "graph list", args: []string{"memory", "admin", "graph", "list"}, wantErrHas: `unknown subcommand "graph"`},
		{name: "graph parent", args: []string{"memory", "admin", "graph"}, wantErrHas: `unknown subcommand "graph"`},
		{name: "session label", args: []string{"session", "label"}, wantErrHas: `unknown subcommand "label"`},
		{name: "session list with label", args: []string{"session", "list", "--label", "foo"}, wantErrHas: "unknown flag: --label"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithSession(&sessionUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute() error = nil, want %q", tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute() error = %q, want substring %q", err.Error(), tt.wantErrHas)
			}
			if strings.Contains(stderr.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice on stderr:\n%s", stderr.String())
			}
			if strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice on stdout:\n%s", stdout.String())
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

func TestRootCLI_SessionsBareMatchesSnapshot(t *testing.T) {
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

	bareStdout, bareStderr, err := run("sessions", "--db-path", "/tmp/traceary-sessions-bare.db")
	if err != nil {
		t.Fatalf("bare sessions error = %v", err)
	}
	snapshotStdout, snapshotStderr, err := run("sessions", "--db-path", "/tmp/traceary-sessions-bare.db", "--snapshot")
	if err != nil {
		t.Fatalf("sessions --snapshot error = %v", err)
	}
	if diff := cmp.Diff(snapshotStdout, bareStdout); diff != "" {
		t.Errorf("bare sessions stdout differs from snapshot (-snapshot +bare):\n%s", diff)
	}
	if diff := cmp.Diff("", bareStderr+snapshotStderr); diff != "" {
		t.Errorf("sessions emitted stderr (-want +got):\n%s", diff)
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
