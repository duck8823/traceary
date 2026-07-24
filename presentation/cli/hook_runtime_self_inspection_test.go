package cli_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_HookAuditCommand_SuppressesSelfInspectionNoise(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		payload string
	}{
		{
			name:    "environment opt-out suppresses normal command audit",
			env:     "1",
			payload: `{"session_id":"generated-session","tool_input":{"command":"go test ./..."},"tool_response":{"stdout":"ok"}}`,
		},
		{
			name:    "command prefix opt-out suppresses traceary read command",
			payload: `{"session_id":"generated-session","tool_input":{"command":"TRACEARY_NO_AUDIT=1 traceary list --json"},"tool_response":{"stdout":"[]"}}`,
		},
		{
			name:    "traceary read MCP list_events suppresses large tool output",
			payload: `{"session_id":"generated-session","tool_name":"mcp__traceary__list_events","tool_input":{"limit":1000},"tool_response":{"content":"large JSON omitted from audits"}}`,
		},
		{
			name:    "traceary CLI self-inspection read command suppresses audit",
			payload: `{"session_id":"generated-session","tool_input":{"command":"traceary sessions --snapshot --json"},"tool_response":{"stdout":"{\"sessions\":[]}"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRACEARY_HOOK_STATE_KEY", "self-noise-key")
			if tc.env != "" {
				t.Setenv("TRACEARY_NO_AUDIT", tc.env)
			}

			homeDir := t.TempDir()
			cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
			t.Cleanup(cli.ResetUserHomeDirFunc)

			eventStub := &eventUsecaseStub{auditErr: errors.New("audit should not be called")}
			rootCmd := newTestRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithEvent(eventStub),
			).Command()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetIn(strings.NewReader(tc.payload))
			rootCmd.SetArgs([]string{"hook", "audit", "codex"})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if eventStub.auditCall.command != "" {
				t.Fatalf("audit command = %q, want no audit call", eventStub.auditCall.command)
			}
		})
	}
}

func TestRootCLI_HookAuditCommand_RecordsGenericSearchTool(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"generated-session","tool_name":"search","tool_input":{"query":"traceary"},"tool_response":{"content":"ok"}}`))
	rootCmd.SetArgs([]string{"hook", "audit", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := eventStub.auditCall.command, "search"; got != want {
		t.Fatalf("audit command = %q, want %q", got, want)
	}
}
