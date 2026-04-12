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

func TestRootCLI_AuditCommand(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDOf("event-1")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-env")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"go test ./...",
		"stdin",
		"stdout",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event: &eventUsecaseStub{
			auditEvent: model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
			),
			auditAudit: commandAudit,
		},
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"--workspace", "duck8823/traceary",
		"go test ./...",
		"stdin",
		"stdout",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() != "Recorded: event-1\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "Recorded: event-1\n")
	}
}

func TestRootCLI_AuditCommand_FallsBackFromStaleActiveSession(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDOf("event-stale-audit")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"go test ./...",
		"stdin",
		"stdout",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{
		activeEvent: model.EventOf(
			mustEventID(t, "event-stale-session"),
			types.EventKindSessionStarted,
			"cli",
			agent,
			mustSessionID(t, "session-stale"),
			"github.com/duck8823/traceary",
			"session started",
			time.Now().Add(-25*time.Hour),
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event: &eventUsecaseStub{
			auditEvent: model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				mustSessionID(t, "default"),
				"github.com/duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 7, 18, 0, 0, 0, time.UTC),
			),
			auditAudit: commandAudit,
		},
		Session: sessionStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"go test ./...",
		"stdin",
		"stdout",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "" +
		"Active session session-stale is stale; using default session ID\n" +
		"Recorded: event-stale-audit\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func mustEventID(t *testing.T, value string) types.EventID {
	t.Helper()

	eventID, err := types.EventIDOf(value)
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}

	return eventID
}

func TestRootCLI_AuditCommand_IdOnly(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDOf("event-id-only")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-env")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"go test ./...",
		"stdin",
		"stdout",
		false,
		true,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}
	commandAudit.SetRedaction(true, true)

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event: &eventUsecaseStub{
			auditEvent: model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
			),
			auditAudit: commandAudit,
		},
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--id-only",
		"go test ./...",
		"stdin",
		"stdout",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() != "event-id-only\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "event-id-only\n")
	}
}

func TestRootCLI_AuditCommand_NamedFlags(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDOf("event-5")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-env")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"go test ./...",
		"",
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event: &eventUsecaseStub{
			auditEvent: model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 7, 16, 0, 0, 0, time.UTC),
			),
			auditAudit: commandAudit,
		},
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--command", "go test ./...",
		"--input", "",
		"--output", "",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootCLI_AuditCommand_AllowsOmittedInputAndOutput(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID := mustEventID(t, "event-audit-optional")
	agent := mustAgent(t, "codex")
	sessionID := mustSessionID(t, "session-env")
	commandAudit, err := model.NewCommandAudit(eventID, "go test ./...", "", "", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event:            &eventUsecaseStub{auditEvent: model.EventOf(eventID, types.EventKindCommandExecuted, "cli", agent, sessionID, "duck8823/traceary", "go test ./...", time.Date(2026, 4, 7, 16, 30, 0, 0, time.UTC)), auditAudit: commandAudit},
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"audit", "--db-path", "/tmp/traceary.db", "go test ./..."})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootCLI_AuditCommand_JSON(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID := mustEventID(t, "event-audit-json")
	agent := mustAgent(t, "codex")
	sessionID := mustSessionID(t, "session-env")
	commandAudit, err := model.NewCommandAudit(eventID, "go test ./...", "stdin", "stdout", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event:            &eventUsecaseStub{auditEvent: model.EventOf(eventID, types.EventKindCommandExecuted, "cli", agent, sessionID, "duck8823/traceary", "go test ./...", time.Date(2026, 4, 7, 17, 0, 0, 0, time.UTC)), auditAudit: commandAudit},
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"audit", "--db-path", "/tmp/traceary.db", "--json", "go test ./...", "stdin", "stdout"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	payload := decodeJSONMap(t, stdout.String())
	eventPayload, ok := payload["event"].(map[string]any)
	if !ok {
		t.Fatalf("event payload = %#v, want object", payload["event"])
	}
	if got, want := eventPayload["event_id"], "event-audit-json"; got != want {
		t.Fatalf("event.event_id = %v, want %q", got, want)
	}
	commandAuditPayload, ok := payload["command_audit"].(map[string]any)
	if !ok {
		t.Fatalf("command_audit payload = %#v, want object", payload["command_audit"])
	}
	if got, want := commandAuditPayload["command"], "go test ./..."; got != want {
		t.Fatalf("command_audit.command = %v, want %q", got, want)
	}
}

func TestRootCLI_AuditCommand_DoesNotAllowDuplicateFlagAndPositional(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event:            &eventUsecaseStub{},
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--command", "go test ./...",
		"go test ./...",
		"stdin",
		"stdout",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("--command")) {
		t.Fatalf("error = %v, want --command reference", err)
	}
}

func TestRootCLI_AuditCommand_TruncationNotice(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")
	t.Setenv("TRACEARY_MAX_AUDIT_OUTPUT_BYTES", "128")

	eventID, err := types.EventIDOf("event-2")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-env")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"go test ./...",
		"stdin",
		"stdout",
		false,
		true,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event: &eventUsecaseStub{
			auditEvent: model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
			),
			auditAudit: commandAudit,
		},
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"--workspace", "duck8823/traceary",
		"go test ./...",
		"stdin",
		"stdout",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "" +
		"Recorded: event-2\n" +
		"Output was truncated before storing\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRootCLI_AuditCommand_RedactionNotice(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDOf("event-3")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-env")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"curl https://example.test",
		`{"access_token":"[REDACTED]"}`,
		"Authorization: Bearer [REDACTED]",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}
	commandAudit.SetRedaction(true, true)

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event: &eventUsecaseStub{
			auditEvent: model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"curl https://example.test",
				time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
			),
			auditAudit: commandAudit,
		},
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"curl https://example.test",
		`{"access_token":"top-secret"}`,
		"Authorization: Bearer token-value",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "" +
		"Recorded: event-3\n" +
		"Input was redacted before storing\n" +
		"Output was redacted before storing\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRootCLI_AuditCommand_AllowSecretsEnv(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")
	t.Setenv("TRACEARY_ALLOW_SECRETS", "true")

	eventID, err := types.EventIDOf("event-4")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-env")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"curl https://example.test",
		`{"access_token":"top-secret"}`,
		"Authorization: Bearer token-value",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		StoreManagement: &storeManagementUsecaseStub{},
		Event: &eventUsecaseStub{
			auditEvent: model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"curl https://example.test",
				time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
			),
			auditAudit: commandAudit,
		},
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"curl https://example.test",
		`{"access_token":"top-secret"}`,
		"Authorization: Bearer token-value",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}
