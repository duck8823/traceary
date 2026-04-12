package cli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

type mcpServerRunnerStub struct {
	called bool
	err    error
}

func (s *mcpServerRunnerStub) Run(_ context.Context) error {
	s.called = true
	return s.err
}

func TestRootCLI_MCPServer(t *testing.T) {
	t.Parallel()

	t.Run("starts MCP server", func(t *testing.T) {
		t.Parallel()

		runner := &mcpServerRunnerStub{}
		var observedDBPath string
		sut := cli.NewRootCLI(
			cli.WithMCPServerRunner(runner),
			cli.WithDatabasePathSetter(func(resolved string) { observedDBPath = resolved }),
		)
		command := sut.Command()
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		command.SetOut(stdout)
		command.SetErr(stderr)
		command.SetArgs([]string{"mcp-server", "--db-path", "./traceary.db"})

		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
		if !runner.called {
			t.Fatalf("runner.Run was not called")
		}
		if observedDBPath == "" {
			t.Fatalf("DatabasePathSetter did not receive the resolved path")
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
