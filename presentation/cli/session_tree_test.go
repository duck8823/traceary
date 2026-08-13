package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// session tree / session lineage were retired in v0.35.0 (#1869). They must
// fail as unknown subcommands with no DEPRECATED shim. Keep sessions --snapshot.
func TestRootCLI_SessionTreeAndLineageAreUnknownSubcommands(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		// Drive the leaf path without extra flags so cobra reports the
		// unknown subcommand rather than an incidental unknown flag.
		{name: "session tree", args: []string{"session", "tree"}, wantErrHas: `unknown subcommand "tree"`},
		{name: "session lineage", args: []string{"session", "lineage"}, wantErrHas: `unknown subcommand "lineage"`},
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
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
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
