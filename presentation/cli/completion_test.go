package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_CompletionCommand(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"completion", "bash"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "complete -o default -F __start_traceary traceary") {
		t.Fatalf("stdout = %q, want bash completion header", stdout.String())
	}
}
