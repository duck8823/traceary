package cli_test

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_ListCommand(t *testing.T) {
	t.Parallel()

	t.Run("displays event list", func(t *testing.T) {
		t.Parallel()

		eventID, err := types.EventIDOf("event-1")
		if err != nil {
			t.Fatalf("EventIDOf() error = %v", err)
		}
		agent, err := types.AgentOf("codex")
		if err != nil {
			t.Fatalf("AgentOf() error = %v", err)
		}
		sessionID, err := types.SessionIDOf("session-1")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}

		listStub := &eventUsecaseStub{
			listEvents: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello",
					time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(listStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"list",
			"--db-path",
			"/tmp/test-traceary.db",
			"--limit", "5",
			"--offset", "2",
			"--kind", "note",
			"--client", "cli",
			"--agent", "codex",
			"--session-id", "session-1",
			"--workspace", "duck8823/traceary",
			"--wide",
			"--utc",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tMESSAGE\n" +
			"2026-04-07T12:00:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\thello\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("displays event list in JSON format", func(t *testing.T) {
		t.Parallel()

		eventID, err := types.EventIDOf("event-2")
		if err != nil {
			t.Fatalf("EventIDOf() error = %v", err)
		}
		agent, err := types.AgentOf("codex")
		if err != nil {
			t.Fatalf("AgentOf() error = %v", err)
		}
		sessionID, err := types.SessionIDOf("session-2")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}

		listStub := &eventUsecaseStub{
			listEvents: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello json",
					time.Date(2026, 4, 7, 12, 30, 0, 0, time.UTC),
				),
			},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(listStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--json"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		want := "" +
			"[\n" +
			"  {\n" +
			"    \"event_id\": \"event-2\",\n" +
			"    \"kind\": \"note\",\n" +
			"    \"client\": \"cli\",\n" +
			"    \"agent\": \"codex\",\n" +
			"    \"session_id\": \"session-2\",\n" +
			"    \"workspace\": \"duck8823/traceary\",\n" +
			"    \"message\": \"hello json\",\n" +
			"    \"created_at\": \"2026-04-07T12:30:00Z\"\n" +
			"  }\n" +
			"]\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("displays message when no events exist", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		listStub := &eventUsecaseStub{}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(listStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "No matching records.\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "No matching records.\n")
		}
	})

	t.Run("returns error when offset is negative", func(t *testing.T) {
		t.Parallel()

		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{}),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--offset", "-1"})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
	})

	t.Run("returns error when kind is invalid", func(t *testing.T) {
		t.Parallel()

		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{}),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--kind", "unknown"})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
	})

	t.Run("--kind audit resolves to command_executed", func(t *testing.T) {
		t.Parallel()

		listStub := &eventUsecaseStub{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(listStub),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--kind", "audit"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	})
}
