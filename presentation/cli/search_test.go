package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SearchCommand(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
			searchEvents: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"github.com/duck8823/traceary",
					"hello traceary",
					time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
				),
			},
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--session-id", "session-1",
		"--client", "cli",
		"--agent", "codex",
		"--kind", "note",
		"--from", "2026-04-07",
		"--since", "2026-04-07",
		"--to", "2026-04-07",
		"--until", "2026-04-07",
		"--limit", "5",
		"--offset", "2",
		"traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() == "" {
		t.Fatalf("stdout is empty")
	}
}

func TestRootCLI_SearchCommand_JSON(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDFrom("event-2")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-2")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
			searchEvents: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"github.com/duck8823/traceary",
					"hello json search",
					time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
				),
			},
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--json",
		"traceary",
	})

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
		"    \"workspace\": \"github.com/duck8823/traceary\",\n" +
		"    \"message\": \"hello json search\",\n" +
		"    \"created_at\": \"2026-04-07T13:00:00Z\"\n" +
		"  }\n" +
		"]\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRootCLI_SearchCommand_FilterOnly(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--session-id", "session-42",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootCLI_SearchCommand_NegativeOffset(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--offset", "-1",
		"traceary",
	})

	if err := rootCmd.Execute(); err == nil {
		t.Fatalf("Execute() error = nil, want error")
	}
}

func TestRootCLI_SearchCommand_FailuresOnlyAsConstraint(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--failures",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; --failures alone should count as a valid search constraint", err)
	}
}
