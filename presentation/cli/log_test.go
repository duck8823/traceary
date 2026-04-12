package cli_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_LogCommand(t *testing.T) {
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

	t.Run("records log with flag values", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreManagement: &storeManagementUsecaseStub{},
			Event: &eventUsecaseStub{
				logEvent: model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello",
					fixedLogTime(),
				),
			},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(stderr)
		rootCmd.SetArgs([]string{
			"log",
			"--db-path",
			"/tmp/test-traceary.db",
			"--client", "cli",
			"--agent", "codex",
			"--session-id", "session-1",
			"--workspace", "duck8823/traceary",
			"hello",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "Recorded: event-1\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "Recorded: event-1\n")
		}
	})

	t.Run("records log with specified kind", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreManagement: &storeManagementUsecaseStub{},
			Event: &eventUsecaseStub{
				logEvent: model.EventOf(
					eventID,
					types.EventKindCompactSummary,
					"hook",
					agent,
					sessionID,
					"duck8823/traceary",
					"summary text",
					fixedLogTime(),
				),
			},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"log",
			"--db-path", "/tmp/test-traceary.db",
			"--kind", "compact_summary",
			"--client", "hook",
			"--agent", "codex",
			"--session-id", "session-1",
			"summary text",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "Recorded: event-1\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "Recorded: event-1\n")
		}
	})

	t.Run("uses environment variable defaults", func(t *testing.T) {
		t.Setenv("TRACEARY_AGENT", "claude")
		t.Setenv("TRACEARY_SESSION_ID", "session-env")
		t.Setenv("TRACEARY_CLIENT", "hook")
		t.Setenv("TRACEARY_WORKSPACE", "duck8823/traceary")

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreManagement: &storeManagementUsecaseStub{},
			Event: &eventUsecaseStub{
				logEvent: model.EventOf(
					eventID,
					types.EventKindNote,
					"hook",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello",
					fixedLogTime(),
				),
			},
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	})

	t.Run("outputs only event ID with id-only flag", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreManagement: &storeManagementUsecaseStub{},
			Event: &eventUsecaseStub{
				logEvent: model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello",
					fixedLogTime(),
				),
			},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "--id-only", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "event-1\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "event-1\n")
		}
	})

	t.Run("outputs structured JSON", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreManagement: &storeManagementUsecaseStub{},
			Event: &eventUsecaseStub{
				logEvent: model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello",
					fixedLogTime(),
				),
			},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "--session-id", "session-1", "--json", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		payload := decodeJSONMap(t, stdout.String())
		if got, want := payload["event_id"], "event-1"; got != want {
			t.Fatalf("event_id = %v, want %q", got, want)
		}
		if got, want := payload["message"], "hello"; got != want {
			t.Fatalf("message = %v, want %q", got, want)
		}
	})

	t.Run("uses active session by default", func(t *testing.T) {
		t.Setenv("TRACEARY_SESSION_ID", "")
		cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
			return "github.com/duck8823/traceary", nil
		})
		defer cli.ResetDetectRepoContextFunc()

		activeEventID, err := types.EventIDOf("event-session-start")
		if err != nil {
			t.Fatalf("EventIDOf() error = %v", err)
		}
		activeSessionID, err := types.SessionIDOf("session-active")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreManagement: &storeManagementUsecaseStub{},
			Event: &eventUsecaseStub{
				logEvent: model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					activeSessionID,
					"github.com/duck8823/traceary",
					"hello",
					fixedLogTime(),
				),
			},
			Session: &sessionUsecaseStub{
				activeEvent: model.EventOf(
					activeEventID,
					types.EventKindSessionStarted,
					"cli",
					agent,
					activeSessionID,
					"github.com/duck8823/traceary",
					"session started",
					time.Now().Add(-1*time.Hour),
				),
			},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "" +
			"Using active session: session-active\n" +
			"Recorded: event-1\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("falls back to default session when work context is missing", func(t *testing.T) {
		t.Setenv("TRACEARY_SESSION_ID", "")
		cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
			return "", errors.New("no git remote")
		})
		defer cli.ResetDetectRepoContextFunc()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreManagement: &storeManagementUsecaseStub{},
			Event: &eventUsecaseStub{
				logEvent: model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					mustSessionID(t, "default"),
					"",
					"hello",
					fixedLogTime(),
				),
			},
			Session: &sessionUsecaseStub{},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "" +
			"No work context was detected; using default session ID\n" +
			"Recorded: event-1\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})
}

func fixedLogTime() time.Time {
	return time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
}

func mustSessionID(t *testing.T, value string) types.SessionID {
	t.Helper()

	sessionID, err := types.SessionIDOf(value)
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	return sessionID
}
