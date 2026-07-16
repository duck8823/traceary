package cli_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_ShowCommand(t *testing.T) {
	t.Parallel()

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

	t.Run("displays event details", func(t *testing.T) {
		t.Parallel()

		eventDetails, err := apptypes.EventDetailsOf(
			model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
			),
			types.Some(model.CommandAuditOf(
				eventID,
				"go test ./...",
				"stdin",
				"stdout",
				true,
				false,
				types.None[int](),
				false,
			)),
		)
		if err != nil {
			t.Fatalf("EventDetailsOf() error = %v", err)
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{showDetails: eventDetails}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"show", "--db-path", "/tmp/test-traceary.db", "event-1"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		want := "" +
			"EVENT_ID: event-1\n" +
			"KIND: command_executed\n" +
			"CLIENT: cli\n" +
			"AGENT: codex\n" +
			"SESSION_ID: session-1\n" +
			"WORKSPACE: duck8823/traceary\n" +
			"SOURCE_HOOK: -\n" +
			"CREATED_AT: 2026-04-08T12:00:00Z\n" +
			"MESSAGE: go test ./...\n" +
			"\n" +
			"COMMAND: go test ./...\n" +
			"EXIT_CODE: -\n" +
			"INPUT_TRUNCATED: true\n" +
			"INPUT:\n" +
			"stdin\n" +
			"OUTPUT_TRUNCATED: false\n" +
			"OUTPUT:\n" +
			"stdout\n"
		if diff := cmp.Diff(want, stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("displays event without command audit", func(t *testing.T) {
		t.Parallel()

		eventDetails, err := apptypes.EventDetailsOf(
			model.EventOf(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				sessionID,
				"",
				"hello",
				time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
			),
			types.None[*model.CommandAudit](),
		)
		if err != nil {
			t.Fatalf("EventDetailsOf() error = %v", err)
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{showDetails: eventDetails}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"show", "--db-path", "/tmp/test-traceary.db", "event-1"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if bytes.Contains(stdout.Bytes(), []byte("COMMAND:")) {
			t.Fatalf("stdout contains command audit section: %q", stdout.String())
		}
	})

	t.Run("displays event details in JSON format", func(t *testing.T) {
		t.Parallel()

		eventDetails, err := apptypes.EventDetailsOf(
			model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 8, 12, 30, 0, 0, time.UTC),
			),
			types.Some(model.CommandAuditOf(
				eventID,
				"go test ./...",
				"stdin",
				"stdout",
				true,
				false,
				types.None[int](),
				false,
			)),
		)
		if err != nil {
			t.Fatalf("EventDetailsOf() error = %v", err)
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{showDetails: eventDetails}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"show", "--db-path", "/tmp/test-traceary.db", "--json", "event-1"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		want := "" +
			"{\n" +
			"  \"event\": {\n" +
			"    \"event_id\": \"event-1\",\n" +
			"    \"kind\": \"command_executed\",\n" +
			"    \"client\": \"cli\",\n" +
			"    \"agent\": \"codex\",\n" +
			"    \"session_id\": \"session-1\",\n" +
			"    \"workspace\": \"duck8823/traceary\",\n" +
			"    \"message\": \"go test ./...\",\n" +
			"    \"created_at\": \"2026-04-08T12:30:00Z\"\n" +
			"  },\n" +
			"  \"command_audit\": {\n" +
			"    \"command\": \"go test ./...\",\n" +
			"    \"input\": \"stdin\",\n" +
			"    \"output\": \"stdout\",\n" +
			"    \"input_truncated\": true,\n" +
			"    \"output_truncated\": false,\n" +
			"    \"sensitive\": {\n" +
			"      \"matched\": false,\n" +
			"      \"coverage\": \"partial\",\n" +
			"      \"redaction\": \"unknown\",\n" +
			"      \"intent_only\": false,\n" +
			"      \"summary\": \"no sensitive path pattern matched\",\n" +
			"      \"coverage_gap\": \"stdout_truncated\"\n" +
			"    }\n" +
			"  }\n" +
			"}\n"
		if diff := cmp.Diff(want, stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("exit_code is included in JSON and text output when present", func(t *testing.T) {
		t.Parallel()

		eventDetails, err := apptypes.EventDetailsOf(
			model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC),
			),
			types.Some(model.CommandAuditOf(
				eventID,
				"go test ./...",
				"stdin",
				"stderr",
				false,
				false,
				types.Some(1),
				false,
			)),
		)
		if err != nil {
			t.Fatalf("EventDetailsOf() error = %v", err)
		}

		t.Run("text format", func(t *testing.T) {
			t.Parallel()

			stdout := &bytes.Buffer{}
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithEvent(&eventUsecaseStub{showDetails: eventDetails}),
			).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{"show", "--db-path", "/tmp/test-traceary.db", "event-1"})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !bytes.Contains(stdout.Bytes(), []byte("EXIT_CODE: 1\n")) {
				t.Fatalf("expected EXIT_CODE: 1 in output, got %q", stdout.String())
			}
		})

		t.Run("json format", func(t *testing.T) {
			t.Parallel()

			stdout := &bytes.Buffer{}
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithEvent(&eventUsecaseStub{showDetails: eventDetails}),
			).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{"show", "--db-path", "/tmp/test-traceary.db", "--json", "event-1"})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !bytes.Contains(stdout.Bytes(), []byte(`"exit_code": 1`)) {
				t.Fatalf("expected exit_code in JSON output, got %q", stdout.String())
			}
		})
	})
}
