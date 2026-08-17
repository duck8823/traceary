package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

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
		{name: "session list", args: []string{"session", "list"}, wantErrHas: `unknown subcommand "list"`},
		{name: "session list with label", args: []string{"session", "list", "--label", "foo"}, wantErrHas: "unknown flag: --label"},
		{name: "session latest", args: []string{"session", "latest"}, wantErrHas: `unknown subcommand "latest"`},
		{name: "session latest --active", args: []string{"session", "latest", "--active"}, wantErrHas: "unknown flag: --active"},
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

// Removed in v0.35.0 after the v0.34 deprecation window (#1688 / #1690).
// top must fail as an unknown command with no DEPRECATED shim.
func TestRootCLI_TopIsUnknownCommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare top", args: []string{"top"}},
		{name: "top snapshot", args: []string{"top", "--snapshot"}},
		{name: "top snapshot json", args: []string{"top", "--snapshot", "--json"}},
		{name: "top help", args: []string{"top", "--help"}},
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
				t.Fatalf("Execute(%v) error = nil, want unknown-command error", tt.args)
			}
			if !strings.Contains(err.Error(), `unknown command "top"`) {
				t.Fatalf("Execute(%v) error = %q, want unknown command \"top\"", tt.args, err.Error())
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

func TestRootCLI_SessionsIsUnknownCommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare sessions", args: []string{"sessions"}},
		{name: "sessions snapshot", args: []string{"sessions", "--snapshot"}},
		{name: "sessions snapshot json", args: []string{"sessions", "--snapshot", "--json"}},
		{name: "sessions help", args: []string{"sessions", "--help"}},
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
				t.Fatalf("Execute(%v) error = nil, want unknown-command error", tt.args)
			}
			if !strings.Contains(err.Error(), `unknown command "sessions"`) {
				t.Fatalf("Execute(%v) error = %q, want unknown command \"sessions\"", tt.args, err.Error())
			}
			if strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
			}
		})
	}
}
