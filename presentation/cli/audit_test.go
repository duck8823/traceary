package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type recordCommandAuditUsecaseStub struct {
	receivedInput usecase.RecordCommandAuditInput
	called        bool
	event         *model.Event
	commandAudit  *model.CommandAudit
	err           error
}

func (s *recordCommandAuditUsecaseStub) Run(
	_ context.Context,
	input usecase.RecordCommandAuditInput,
) (*model.Event, *model.CommandAudit, error) {
	s.called = true
	s.receivedInput = input
	return s.event, s.commandAudit, s.err
}

var _ usecase.RecordCommandAuditUsecase = (*recordCommandAuditUsecaseStub)(nil)

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

	initStub := &initializeStoreUsecaseStub{}
	auditStub := &recordCommandAuditUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"go test ./...",
			time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
		),
		commandAudit: commandAudit,
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:    initStub,
		RecordCommandAuditUsecase: auditStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"--repo", "duck8823/traceary",
		"go test ./...",
		"stdin",
		"stdout",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !auditStub.called {
		t.Fatalf("RecordCommandAuditUsecase.Run() was not called")
	}
	if auditStub.receivedInput.Command != "go test ./..." {
		t.Fatalf("Command = %q, want %q", auditStub.receivedInput.Command, "go test ./...")
	}
	if auditStub.receivedInput.SessionID != "session-env" {
		t.Fatalf("SessionID = %q, want %q", auditStub.receivedInput.SessionID, "session-env")
	}
	if stdout.String() != "Recorded: event-1\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "Recorded: event-1\n")
	}
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

	initStub := &initializeStoreUsecaseStub{}
	auditStub := &recordCommandAuditUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"go test ./...",
			time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
		),
		commandAudit: commandAudit,
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:    initStub,
		RecordCommandAuditUsecase: auditStub,
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

	initStub := &initializeStoreUsecaseStub{}
	auditStub := &recordCommandAuditUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"go test ./...",
			time.Date(2026, 4, 7, 16, 0, 0, 0, time.UTC),
		),
		commandAudit: commandAudit,
	}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:    initStub,
		RecordCommandAuditUsecase: auditStub,
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
	if auditStub.receivedInput.Command != "go test ./..." {
		t.Fatalf("Command = %q, want %q", auditStub.receivedInput.Command, "go test ./...")
	}
	if auditStub.receivedInput.Input != "" {
		t.Fatalf("Input = %q, want empty", auditStub.receivedInput.Input)
	}
	if auditStub.receivedInput.Output != "" {
		t.Fatalf("Output = %q, want empty", auditStub.receivedInput.Output)
	}
}

func TestRootCLI_AuditCommand_DoesNotAllowDuplicateFlagAndPositional(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:    &initializeStoreUsecaseStub{},
		RecordCommandAuditUsecase: &recordCommandAuditUsecaseStub{},
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

	initStub := &initializeStoreUsecaseStub{}
	auditStub := &recordCommandAuditUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"go test ./...",
			time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
		),
		commandAudit: commandAudit,
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:    initStub,
		RecordCommandAuditUsecase: auditStub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"audit",
		"--db-path", "/tmp/traceary.db",
		"--agent", "codex",
		"--client", "cli",
		"--repo", "duck8823/traceary",
		"go test ./...",
		"stdin",
		"stdout",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if auditStub.receivedInput.MaxOutputBytes != 128 {
		t.Fatalf("MaxOutputBytes = %d, want 128", auditStub.receivedInput.MaxOutputBytes)
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

	initStub := &initializeStoreUsecaseStub{}
	auditStub := &recordCommandAuditUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"curl https://example.test",
			time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
		),
		commandAudit: commandAudit,
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:    initStub,
		RecordCommandAuditUsecase: auditStub,
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

	initStub := &initializeStoreUsecaseStub{}
	auditStub := &recordCommandAuditUsecaseStub{
		event: model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"curl https://example.test",
			time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC),
		),
		commandAudit: commandAudit,
	}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:    initStub,
		RecordCommandAuditUsecase: auditStub,
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
	if !auditStub.receivedInput.AllowSecrets {
		t.Fatalf("AllowSecrets = false, want true")
	}
}
