package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_ContextCommand(t *testing.T) {
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

	t.Run("resolves latest session and displays context", func(t *testing.T) {
		t.Parallel()

		contextEvents := []*model.Event{
			model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				sessionID,
				"github.com/duck8823/traceary",
				"README を更新した\n次に release note を確認する",
				time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
			),
		}
		activeEvent := model.EventOf(
			eventID,
			types.EventKindSessionStarted,
			"cli",
			agent,
			sessionID,
			"github.com/duck8823/traceary",
			"session started",
			time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC),
		)

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{contextEvents: contextEvents}),
			cli.WithSession(&sessionUsecaseStub{activeEvent: activeEvent}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"context", "--db-path", "/tmp/test-traceary.db", "--limit", "5"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "" +
			"TRACEARY CONTEXT\n" +
			"SESSION_ID: session-1\n" +
			"WORKSPACE: github.com/duck8823/traceary\n" +
			"EVENTS:\n" +
			"- 2026-04-08T12:00:00Z [note] event-1 cli/codex README を更新した 次に release note を確認する\n"
		if diff := cmp.Diff(want, stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("displays context in JSON format", func(t *testing.T) {
		t.Parallel()

		contextEvents := []*model.Event{
			model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				sessionID,
				"github.com/duck8823/traceary",
				"hello context",
				time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
			),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{contextEvents: contextEvents}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"context",
			"--db-path",
			"/tmp/test-traceary.db",
			"--session-id", "session-1",
			"--json",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		want := "" +
			"{\n" +
			"  \"resolved_session_id\": \"session-1\",\n" +
			"  \"resolved_workspace\": \"github.com/duck8823/traceary\",\n" +
			"  \"events\": [\n" +
			"    {\n" +
			"      \"event_id\": \"event-1\",\n" +
			"      \"kind\": \"note\",\n" +
			"      \"client\": \"cli\",\n" +
			"      \"agent\": \"codex\",\n" +
			"      \"session_id\": \"session-1\",\n" +
			"      \"workspace\": \"github.com/duck8823/traceary\",\n" +
			"      \"message\": \"hello context\",\n" +
			"      \"created_at\": \"2026-04-08T12:00:00Z\"\n" +
			"    }\n" +
			"  ]\n" +
			"}\n"
		if diff := cmp.Diff(want, stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("falls back to repo context when no latest session exists", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")

		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{}),
			cli.WithSession(&sessionUsecaseStub{}),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"context", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	})
}
