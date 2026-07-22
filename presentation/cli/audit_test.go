package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_AuditCommand(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
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
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
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
		}),
	).Command()
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
	if diff := cmp.Diff("Recorded: event-1\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_AuditCommand_FallsBackFromStaleActiveSession(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDFrom("event-stale-audit")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
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
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
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
		}),
		cli.WithSession(sessionStub),
	).Command()
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
	if diff := cmp.Diff(want, stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func mustEventID(t *testing.T, value string) types.EventID {
	t.Helper()

	eventID, err := types.EventIDFrom(value)
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}

	return eventID
}

func TestRootCLI_AuditCommand_IdOnly(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDFrom("event-id-only")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
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
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
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
		}),
	).Command()
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
	if diff := cmp.Diff("event-id-only\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_AuditCommand_NamedFlags(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDFrom("event-5")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
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

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
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
		}),
	).Command()
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

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{auditEvent: model.EventOf(eventID, types.EventKindCommandExecuted, "cli", agent, sessionID, "duck8823/traceary", "go test ./...", time.Date(2026, 4, 7, 16, 30, 0, 0, time.UTC)), auditAudit: commandAudit}),
	).Command()
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
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{auditEvent: model.EventOf(eventID, types.EventKindCommandExecuted, "cli", agent, sessionID, "duck8823/traceary", "go test ./...", time.Date(2026, 4, 7, 17, 0, 0, 0, time.UTC)), auditAudit: commandAudit}),
	).Command()
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

func TestRootCLI_AuditCommand_ForwardsStructuredOutcome(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")
	eventID := mustEventID(t, "event-audit-outcome")
	audit, err := model.NewCommandAudit(eventID, "rtk git status", "", "", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}
	eventStub := &eventUsecaseStub{
		auditEvent: model.EventOf(eventID, types.EventKindCommandExecuted, "cli", "codex", "session-env", "workspace", "rtk git status", time.Now()),
		auditAudit: audit,
	}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"audit", "--db-path", "/tmp/traceary.db", "--exit-code", "124", "--failure-reason", "timeout", "rtk git status"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, ok := eventStub.auditCall.exitCode.Value(); !ok || got != 124 {
		t.Fatalf("exit code = (%d, %v), want (124, true)", got, ok)
	}
	if got := eventStub.auditCall.failureReason; got != types.CommandFailureReasonTimeout {
		t.Fatalf("failure reason = %q, want timeout", got)
	}
}

func TestRootCLI_AuditCommand_RejectsUnknownFailureReason(t *testing.T) {
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"audit", "--db-path", "/tmp/traceary.db", "--failure-reason", "quoted_failure", "go test ./..."})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want invalid failure reason error")
	}
}

func TestRootCLI_AuditCommand_DoesNotAllowDuplicateFlagAndPositional(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
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

	eventID, err := types.EventIDFrom("event-2")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
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
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
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
		}),
	).Command()
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

func TestRootCLI_AuditCommand_UsesConfiguredAuditLimits(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDFrom("event-config-limit")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(eventID, "go test ./...", "stdin", "stdout", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	eventStub := &eventUsecaseStub{
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
	}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithDefaultAuditPayloadLimits(256, 512),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
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
	if got := eventStub.auditCall.auditCfg.MaxInputBytes(); got != 256 {
		t.Fatalf("MaxInputBytes() = %d, want 256", got)
	}
	if got := eventStub.auditCall.auditCfg.MaxOutputBytes(); got != 512 {
		t.Fatalf("MaxOutputBytes() = %d, want 512", got)
	}
}

func TestRootCLI_AuditCommand_ExplicitZeroLimitUsesBuiltinDefault(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDFrom("event-config-zero-limit")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	commandAudit, err := model.NewCommandAudit(eventID, "go test ./...", "stdin", "stdout", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	eventStub := &eventUsecaseStub{
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
	}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithDefaultAuditPayloadLimits(256, 512),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"--workspace", "duck8823/traceary",
		"--max-input-bytes", "0",
		"--max-output-bytes", "0",
		"go test ./...",
		"stdin",
		"stdout",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := eventStub.auditCall.auditCfg.MaxInputBytes(); got != 0 {
		t.Fatalf("MaxInputBytes() = %d, want 0", got)
	}
	if got := eventStub.auditCall.auditCfg.MaxOutputBytes(); got != 0 {
		t.Fatalf("MaxOutputBytes() = %d, want 0", got)
	}
}

func TestRootCLI_AuditCommand_RedactionNotice(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDFrom("event-3")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
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
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
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
		}),
	).Command()
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

	eventID, err := types.EventIDFrom("event-4")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
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

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
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
		}),
	).Command()
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
