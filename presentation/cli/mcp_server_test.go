package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_MCPServerIsUnknownCommand(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"mcp-server"},
		{"mcp-server", "--help"},
		{"mcp-server", "--db-path", "./traceary.db"},
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
			if !strings.Contains(combined, `unknown command "mcp-server"`) {
				t.Fatalf("output = %q, want unknown command \"mcp-server\"", combined)
			}
			if strings.Contains(combined, "deprecated") {
				t.Fatalf("output = %q, must not emit DEPRECATED notice", combined)
			}
		})
	}
}

func TestRootCLI_HelpDoesNotListMCPServer(t *testing.T) {
	t.Parallel()

	sut := cli.NewRootCLI()
	command := sut.Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetArgs([]string{"--help"})

	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	out := stdout.String() + stderr.String()
	if strings.Contains(out, "mcp-server") {
		t.Fatalf("--help listed mcp-server:\n%s", out)
	}
}
