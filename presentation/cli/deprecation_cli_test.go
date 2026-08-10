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
			if err := root.Execute(); err != nil {
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
