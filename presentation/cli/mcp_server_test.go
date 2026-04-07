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

	t.Run("MCP server を起動する", func(t *testing.T) {
		t.Parallel()

		runner := &mcpServerRunnerStub{}
		sut := cli.NewRootCLI(nil, nil, nil, nil, nil, nil, nil, nil, nil, runner)
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

	t.Run("ランナー未設定ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := cli.NewRootCLI(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		command := sut.Command()
		command.SetArgs([]string{"mcp-server"})

		if err := command.ExecuteContext(context.Background()); err == nil {
			t.Fatalf("ExecuteContext() error = nil, want error")
		}
	})
}
