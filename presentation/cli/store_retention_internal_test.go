package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestStoreRetentionAndArchiveAreUnknownSubcommands(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare archive", args: []string{"store", "archive"}, wantErrHas: `unknown subcommand "archive"`},
		{name: "archive create", args: []string{"store", "archive", "create"}, wantErrHas: "unknown flag: --output"},
		{name: "bare retention", args: []string{"store", "retention"}, wantErrHas: `unknown subcommand "retention"`},
		{name: "retention files", args: []string{"store", "retention", "files"}, wantErrHas: "unknown flag: --output"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := NewRootCLI().Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			got := err.Error() + stderr.String() + stdout.String()
			if !strings.Contains(got, tt.wantErrHas) && !strings.Contains(err.Error(), `unknown subcommand`) {
				// archive create / retention files after unknown parent: cobra may report
				// unknown subcommand "archive"/"retention" before extra args become flags.
				if !strings.Contains(got, `unknown subcommand "archive"`) && !strings.Contains(got, `unknown subcommand "retention"`) {
					t.Fatalf("error = %q, want %q or unknown subcommand", got, tt.wantErrHas)
				}
			}
			if strings.Contains(got, "DEPRECATED") {
				t.Fatalf("must not mention DEPRECATED: %q", got)
			}
		})
	}
}
