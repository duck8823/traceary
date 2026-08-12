package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_ListCommand(t *testing.T) {
	t.Parallel()

	t.Run("displays event list", func(t *testing.T) {
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
		want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tSOURCE_HOOK\tMESSAGE\n" +
			"2026-04-07T12:00:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\t-\thello\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("displays event list in JSON format", func(t *testing.T) {
		t.Parallel()

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

	t.Run("--sensitive keeps only sensitive command audits", func(t *testing.T) {
		t.Parallel()

		agent, err := types.AgentFrom("codex")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}
		sessionID, err := types.SessionIDFrom("session-1")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		mk := func(id, command, output string) *model.Event {
			t.Helper()
			eventID, err := types.EventIDFrom(id)
			if err != nil {
				t.Fatalf("EventIDFrom(%s) error = %v", id, err)
			}
			// This new command_executed fixture has an empty body; classification
			// uses the attached audit.
			event := model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"",
				time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
			)
			audit, err := model.NewCommandAudit(eventID, command, "", output, false, false)
			if err != nil {
				t.Fatalf("NewCommandAudit(%s) error = %v", id, err)
			}
			event.AttachCommandAudit(audit)
			return event
		}
		listStub := &eventUsecaseStub{listEvents: []*model.Event{
			mk("event-sensitive", "cat .env", ""),
			mk("event-normal", "go test ./...", "ok"),
		}}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(listStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--sensitive", "--json"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(stdout.String(), "event-sensitive") {
			t.Fatalf("stdout missing sensitive event: %s", stdout.String())
		}
		if strings.Contains(stdout.String(), "event-normal") {
			t.Fatalf("stdout should filter ordinary audit: %s", stdout.String())
		}
		if listStub.hydrateCalls != 1 {
			t.Fatalf("hydrateCalls = %d, want 1 (full payload only)", listStub.hydrateCalls)
		}
		wantFields := queryservice.FullCommandAuditPayload()
		if diff := cmp.Diff(wantFields, listStub.lastHydrateFields); diff != "" {
			t.Fatalf("lastHydrateFields mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("command_executed empty body lists full command line text and JSON message", func(t *testing.T) {
		t.Parallel()

		agent, err := types.AgentFrom("codex")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}
		sessionID, err := types.SessionIDFrom("session-1")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		eventID, err := types.EventIDFrom("event-cmd-line")
		if err != nil {
			t.Fatalf("EventIDFrom() error = %v", err)
		}
		const (
			fullCommand = "go test ./..."
			commandName = "go"
		)
		// Metadata-only listing attach: empty command_text, command_name only.
		// Matches the #1675 list JOIN surface before command-only hydration.
		event := model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"",
			time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
		)
		audit, err := model.CommandAuditFromListingMetadata(model.CommandAuditSnapshot{
			EventID:       eventID,
			Command:       "",
			CommandName:   types.CommandName(commandName),
			FailureReason: types.CommandFailureReasonNone,
		})
		if err != nil {
			t.Fatalf("CommandAuditFromListingMetadata() error = %v", err)
		}
		event.AttachCommandAudit(audit)

		listStub := &eventUsecaseStub{
			listEvents:              []*model.Event{event},
			hydrateCommandByEventID: map[string]string{eventID.String(): fullCommand},
		}

		// Text listing must show the full command line, not the basename.
		textOut := &bytes.Buffer{}
		textCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(listStub),
		).Command()
		textCmd.SetOut(textOut)
		textCmd.SetErr(&bytes.Buffer{})
		textCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--wide"})
		if err := textCmd.Execute(); err != nil {
			t.Fatalf("list text Execute() error = %v", err)
		}
		if !strings.Contains(textOut.String(), fullCommand) {
			t.Fatalf("list text missing full command line %q; got:\n%s", fullCommand, textOut.String())
		}
		// Basename alone without the rest of the line is the bug this pins.
		if strings.Contains(textOut.String(), " "+commandName+"\n") && !strings.Contains(textOut.String(), fullCommand) {
			t.Fatalf("list text degraded to command_name only: %s", textOut.String())
		}

		// Reset hydrate counters for the JSON pass; re-list same stub events.
		listStub.hydrateCalls = 0
		// Re-attach metadata-only audit: prior text run hydrated the event in place.
		auditAgain, err := model.CommandAuditFromListingMetadata(model.CommandAuditSnapshot{
			EventID:       eventID,
			Command:       "",
			CommandName:   types.CommandName(commandName),
			FailureReason: types.CommandFailureReasonNone,
		})
		if err != nil {
			t.Fatalf("CommandAuditFromListingMetadata() reset error = %v", err)
		}
		event.AttachCommandAudit(auditAgain)

		jsonOut := &bytes.Buffer{}
		jsonCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(listStub),
		).Command()
		jsonCmd.SetOut(jsonOut)
		jsonCmd.SetErr(&bytes.Buffer{})
		jsonCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--json"})
		if err := jsonCmd.Execute(); err != nil {
			t.Fatalf("list json Execute() error = %v", err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
			t.Fatalf("json.Unmarshal() error = %v\nraw: %s", err, jsonOut.String())
		}
		if len(rows) != 1 {
			t.Fatalf("json rows len = %d, want 1; raw: %s", len(rows), jsonOut.String())
		}
		gotMessage, _ := rows[0]["message"].(string)
		if diff := cmp.Diff(fullCommand, gotMessage); diff != "" {
			t.Fatalf("json message mismatch (-want +got):\n%s", diff)
		}
		if listStub.hydrateCalls != 1 {
			t.Fatalf("json hydrateCalls = %d, want 1", listStub.hydrateCalls)
		}
		wantFields := queryservice.CommandOnlyPayload()
		if diff := cmp.Diff(wantFields, listStub.lastHydrateFields); diff != "" {
			t.Fatalf("json lastHydrateFields mismatch (-want +got):\n%s", diff)
		}
	})
}
