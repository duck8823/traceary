package cli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

type mcpServerRunnerStub struct {
	receivedPath string
	err          error
}

func (s *mcpServerRunnerStub) Run(_ context.Context, dbPath string) error {
	s.receivedPath = dbPath
	return s.err
}

func TestRootCLI_MCPServer(t *testing.T) {
	t.Parallel()

	t.Run("starts MCP server", func(t *testing.T) {
		t.Parallel()

		runner := &mcpServerRunnerStub{}
		sut := cli.NewRootCLI(cli.WithMCPServerRunner(runner))
		command := sut.Command()
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		command.SetOut(stdout)
		command.SetErr(stderr)
		command.SetArgs([]string{"mcp-server", "--db-path", "./traceary.db"})

		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
		if runner.receivedPath == "" {
			t.Fatalf("received path is empty")
		}
	})

	t.Run("returns error when runner is not configured", func(t *testing.T) {
		t.Parallel()

		sut := cli.NewRootCLI()
		command := sut.Command()
		command.SetArgs([]string{"mcp-server"})

		if err := command.ExecuteContext(context.Background()); err == nil {
			t.Fatalf("ExecuteContext() error = nil, want error")
		}
	})
}
