package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_AttestIsUnknownCommand(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"attest"},
		{"attest", "--help"},
		{"attest", "verify"},
	} {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			sut := cli.NewRootCLI()
			command := sut.Command()
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			command.SetOut(stdout)
			command.SetErr(stderr)
			command.SetArgs(args)

			err := command.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("ExecuteContext() error = nil, want unknown command")
			}
			combined := strings.ToLower(err.Error() + "\n" + stderr.String() + stdout.String())
			if !strings.Contains(combined, `unknown command "attest"`) {
				t.Fatalf("output = %q, want unknown command \"attest\"", combined)
			}
			if strings.Contains(combined, "deprecated") {
				t.Fatalf("output = %q, must not emit DEPRECATED notice", combined)
			}
		})
	}
}
