package cli_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_LogCommand(t *testing.T) {
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

	t.Run("records log with flag values", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
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
			}),
		).Command()
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
		if diff := cmp.Diff("Recorded: event-1\n", stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("records log with specified kind", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
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
			}),
		).Command()
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
		if diff := cmp.Diff("Recorded: event-1\n", stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("uses environment variable defaults", func(t *testing.T) {
		t.Setenv("TRACEARY_AGENT", "claude")
		t.Setenv("TRACEARY_SESSION_ID", "session-env")
		t.Setenv("TRACEARY_CLIENT", "hook")
		t.Setenv("TRACEARY_WORKSPACE", "duck8823/traceary")

		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
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
			}),
		).Command()
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
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
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
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "--id-only", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if diff := cmp.Diff("event-1\n", stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("outputs structured JSON", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
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
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "--session-id", "session-1", "--json", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		payload := decodeJSONMap(t, stdout.String())
		if diff := cmp.Diff("event-1", payload["event_id"]); diff != "" {
			t.Fatalf("event_id mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("hello", payload["message"]); diff != "" {
			t.Fatalf("message mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("uses active session by default", func(t *testing.T) {
		t.Setenv("TRACEARY_SESSION_ID", "")
		cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
			return "github.com/duck8823/traceary", nil
		})
		defer cli.ResetDetectRepoContextFunc()

		activeEventID, err := types.EventIDFrom("event-session-start")
		if err != nil {
			t.Fatalf("EventIDFrom() error = %v", err)
		}
		activeSessionID, err := types.SessionIDFrom("session-active")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
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
			}),
			cli.WithSession(&sessionUsecaseStub{
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
			}),
		).Command()
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

	t.Run("falls back to default session when workspace is missing", func(t *testing.T) {
		t.Setenv("TRACEARY_SESSION_ID", "")
		cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
			return "", errors.New("no git remote")
		})
		defer cli.ResetDetectRepoContextFunc()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
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
			}),
			cli.WithSession(&sessionUsecaseStub{}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"log", "--db-path", "/tmp/test-traceary.db", "hello"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "" +
			"No workspace was detected; using default session ID\n" +
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

	sessionID, err := types.SessionIDFrom(value)
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	return sessionID
}
