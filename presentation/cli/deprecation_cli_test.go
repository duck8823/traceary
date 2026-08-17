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
		{name: "session handoff", args: []string{"session", "handoff"}, wantErrHas: `unknown subcommand "handoff"`},
		{name: "session handoff --compact-only", args: []string{"session", "handoff", "--compact-only"}, wantErrHas: "unknown flag: --compact-only"},
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

func TestRootCLI_StoreArchiveAndRetentionAreUnknownSubcommands(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare archive", args: []string{"store", "archive"}, wantErrHas: `unknown subcommand "archive"`},
		{name: "archive create", args: []string{"store", "archive", "create"}, wantErrHas: `unknown subcommand "archive"`},
		{name: "bare retention", args: []string{"store", "retention"}, wantErrHas: `unknown subcommand "retention"`},
		{name: "retention files", args: []string{"store", "retention", "files"}, wantErrHas: `unknown subcommand "retention"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_StoreSearchProjectionIsUnknownSubcommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare group", args: []string{"store", "search-projection"}, wantErrHas: `unknown subcommand "search-projection"`},
		{name: "start", args: []string{"store", "search-projection", "start"}, wantErrHas: `unknown subcommand "search-projection"`},
		{name: "resume", args: []string{"store", "search-projection", "resume"}, wantErrHas: `unknown subcommand "search-projection"`},
		{name: "status", args: []string{"store", "search-projection", "status"}, wantErrHas: `unknown subcommand "search-projection"`},
		{name: "abort", args: []string{"store", "search-projection", "abort"}, wantErrHas: `unknown subcommand "search-projection"`},
		{name: "probe", args: []string{"store", "search-projection", "probe"}, wantErrHas: `unknown subcommand "search-projection"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_StoreInitIsUnknownSubcommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare init", args: []string{"store", "init"}, wantErrHas: `unknown subcommand "init"`},
		{name: "init with db-path", args: []string{"store", "init", "--db-path", "x.db"}, wantErrHas: `unknown flag: --db-path`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_StoreWorkspaceAliasIsUnknownSubcommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare workspace-alias", args: []string{"store", "workspace-alias"}, wantErrHas: `unknown subcommand "workspace-alias"`},
		{name: "workspace-alias add", args: []string{"store", "workspace-alias", "add"}, wantErrHas: `unknown subcommand "workspace-alias"`},
		{name: "workspace-alias list", args: []string{"store", "workspace-alias", "list"}, wantErrHas: `unknown subcommand "workspace-alias"`},
		{name: "workspace-alias remove", args: []string{"store", "workspace-alias", "remove"}, wantErrHas: `unknown subcommand "workspace-alias"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_StoreCapacityIsUnknownSubcommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare capacity", args: []string{"store", "capacity"}, wantErrHas: `unknown subcommand "capacity"`},
		{name: "capacity json", args: []string{"store", "capacity", "--json"}, wantErrHas: "unknown flag: --json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_MemoryListIsUnknownSubcommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare list", args: []string{"memory", "list"}, wantErrHas: `unknown subcommand "list"`},
		{name: "list json", args: []string{"memory", "list", "--json"}, wantErrHas: "unknown flag: --json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemory(&memoryUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_HooksPrintIsUnknownSubcommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare print", args: []string{"hooks", "print"}, wantErrHas: `unknown subcommand "print"`},
		{name: "print client", args: []string{"hooks", "print", "--client", "claude"}, wantErrHas: "unknown flag: --client"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_ReplayIsUnknownCommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare replay", args: []string{"replay"}},
		{name: "replay help", args: []string{"replay", "--help"}},
		{name: "replay out", args: []string{"replay", "--out", "x.html"}},
		{name: "replay format markdown", args: []string{"replay", "--format", "markdown", "--out", "x.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want unknown-command error", tt.args)
			}
			if !strings.Contains(err.Error(), `unknown command "replay"`) {
				t.Fatalf("Execute(%v) error = %q, want unknown command \"replay\"", tt.args, err.Error())
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_TailIsUnknownCommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare tail", args: []string{"tail"}},
		{name: "tail json", args: []string{"tail", "--json"}},
		{name: "tail help", args: []string{"tail", "--help"}},
		{name: "tail follow-session", args: []string{"tail", "--follow-session", "sess0001"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithEvent(&eventUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want unknown-command error", tt.args)
			}
			if !strings.Contains(err.Error(), `unknown command "tail"`) {
				t.Fatalf("Execute(%v) error = %q, want unknown command \"tail\"", tt.args, err.Error())
			}
			if strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_TimelineIsUnknownCommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare timeline", args: []string{"timeline"}},
		{name: "timeline json", args: []string{"timeline", "--json"}},
		{name: "timeline help", args: []string{"timeline", "--help"}},
		{name: "timeline workspace", args: []string{"timeline", "--workspace", "ws"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithEvent(&eventUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want unknown-command error", tt.args)
			}
			if !strings.Contains(err.Error(), `unknown command "timeline"`) {
				t.Fatalf("Execute(%v) error = %q, want unknown command \"timeline\"", tt.args, err.Error())
			}
			if strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
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
