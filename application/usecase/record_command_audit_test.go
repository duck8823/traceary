package usecase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type commandAuditSaverStub struct {
	savedEvent        *model.Event
	savedCommandAudit *model.CommandAudit
	err               error
}

func (s *commandAuditSaverStub) Save(_ context.Context, event *model.Event) error {
	s.savedEvent = event
	return s.err
}

func (s *commandAuditSaverStub) SaveWithAudit(
	_ context.Context,
	event *model.Event,
	commandAudit *model.CommandAudit,
) error {
	s.savedEvent = event
	s.savedCommandAudit = commandAudit
	return s.err
}

func TestEventUsecase_Audit(t *testing.T) {
	t.Parallel()

	t.Run("saves audit event successfully", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		event, commandAudit, err := sut.Audit(context.Background(),
			"go test ./...",
			"stdin",
			"stdout",
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			types.None[int](),
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if event == nil || commandAudit == nil {
			t.Fatalf("Audit() returned nil values")
		}
		if stub.savedEvent != event {
			t.Fatalf("saved event mismatch")
		}
		if stub.savedCommandAudit != commandAudit {
			t.Fatalf("saved command audit mismatch")
		}
		if diff := cmp.Diff("command_executed", event.Kind().String()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("go test ./...", event.Body()); diff != "" {
			t.Fatalf("Body() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("truncates long input/output before saving", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)
		longInput := strings.Repeat("i", 70*1024)
		longOutput := strings.Repeat("o", 70*1024)

		_, commandAudit, err := sut.Audit(context.Background(),
			"go test ./...",
			longInput,
			longOutput,
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace(""),
			types.None[int](),
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if !commandAudit.InputTruncated() {
			t.Fatalf("InputTruncated() = false, want true")
		}
		if !commandAudit.OutputTruncated() {
			t.Fatalf("OutputTruncated() = false, want true")
		}
		if !strings.HasSuffix(commandAudit.Input(), "\n...[truncated]") {
			t.Fatalf("Input() suffix = %q, want truncated suffix", commandAudit.Input()[len(commandAudit.Input())-16:])
		}
		if !strings.HasSuffix(commandAudit.Output(), "\n...[truncated]") {
			t.Fatalf("Output() suffix = %q, want truncated suffix", commandAudit.Output()[len(commandAudit.Output())-16:])
		}
	})

	t.Run("truncates input/output at explicit limit", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, commandAudit, err := sut.Audit(context.Background(),
			"go test ./...",
			strings.Repeat("i", 32),
			strings.Repeat("o", 32),
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace(""),
			types.None[int](),
			apptypes.NewAuditRedactionBuilder().
				MaxInputBytes(16).
				MaxOutputBytes(20).
				Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if diff := cmp.Diff(16, len(commandAudit.Input())); diff != "" {
			t.Fatalf("len(Input()) mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(20, len(commandAudit.Output())); diff != "" {
			t.Fatalf("len(Output()) mismatch (-want +got):\n%s", diff)
		}
		if !commandAudit.InputTruncated() || !commandAudit.OutputTruncated() {
			t.Fatalf("truncated flags = (%t, %t), want both true", commandAudit.InputTruncated(), commandAudit.OutputTruncated())
		}
	})

	t.Run("redacts common secrets by default", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, commandAudit, err := sut.Audit(context.Background(),
			"curl https://example.test",
			`{"access_token":"top-secret","note":"keep"}`,
			"Authorization: Bearer token-value\nexport API_KEY=\"abc123\"",
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace(""),
			types.None[int](),
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if !commandAudit.InputRedacted() {
			t.Fatalf("InputRedacted() = false, want true")
		}
		if !commandAudit.OutputRedacted() {
			t.Fatalf("OutputRedacted() = false, want true")
		}
		if strings.Contains(commandAudit.Input(), "top-secret") {
			t.Fatalf("Input() leaked secret: %q", commandAudit.Input())
		}
		if strings.Contains(commandAudit.Output(), "token-value") || strings.Contains(commandAudit.Output(), "abc123") {
			t.Fatalf("Output() leaked secret: %q", commandAudit.Output())
		}
		if !strings.Contains(commandAudit.Input(), "[REDACTED]") {
			t.Fatalf("Input() = %q, want redaction marker", commandAudit.Input())
		}
		if !strings.Contains(commandAudit.Output(), "[REDACTED]") {
			t.Fatalf("Output() = %q, want redaction marker", commandAudit.Output())
		}
	})

	t.Run("saves raw payload when allow secrets is enabled", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, commandAudit, err := sut.Audit(context.Background(),
			"curl https://example.test",
			`{"access_token":"top-secret"}`,
			"Authorization: Bearer token-value",
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace(""),
			types.None[int](),
			apptypes.NewAuditRedactionBuilder().
				AllowSecrets(true).
				MaxInputBytes(256).
				Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if commandAudit.InputRedacted() {
			t.Fatalf("InputRedacted() = true, want false")
		}
		if commandAudit.OutputRedacted() {
			t.Fatalf("OutputRedacted() = true, want false")
		}
		if !strings.Contains(commandAudit.Input(), "top-secret") {
			t.Fatalf("Input() = %q, want raw secret", commandAudit.Input())
		}
		if !strings.Contains(commandAudit.Output(), "token-value") {
			t.Fatalf("Output() = %q, want raw secret", commandAudit.Output())
		}
	})

	t.Run("redacts custom fields with extra patterns", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, commandAudit, err := sut.Audit(context.Background(),
			"curl https://example.test",
			"my_custom_secret=hunter2",
			"internal_token: abc123",
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace(""),
			types.None[int](),
			apptypes.NewAuditRedactionBuilder().
				ExtraRedactPatterns([]string{"my_custom_secret=\\S+", "internal_token:\\s*\\S+"}).
				Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if strings.Contains(commandAudit.Input(), "hunter2") {
			t.Fatalf("Input() leaked custom secret: %q", commandAudit.Input())
		}
		if strings.Contains(commandAudit.Output(), "abc123") {
			t.Fatalf("Output() leaked custom secret: %q", commandAudit.Output())
		}
		if !commandAudit.InputRedacted() || !commandAudit.OutputRedacted() {
			t.Fatalf("redacted flags = (%t, %t), want both true", commandAudit.InputRedacted(), commandAudit.OutputRedacted())
		}
	})

	t.Run("returns error for invalid extra redaction pattern", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, _, err := sut.Audit(context.Background(),
			"test",
			"",
			"",
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace(""),
			types.None[int](),
			apptypes.NewAuditRedactionBuilder().
				ExtraRedactPatterns([]string{"[invalid"}).
				Build(),
		)
		if err == nil {
			t.Fatalf("Audit() error = nil, want error for invalid regex")
		}
	})

	t.Run("returns error for negative limit", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, _, err := sut.Audit(context.Background(),
			"go test ./...",
			"stdin",
			"stdout",
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace(""),
			types.None[int](),
			apptypes.NewAuditRedactionBuilder().
				MaxInputBytes(-1).
				Build(),
		)
		if err == nil {
			t.Fatalf("Audit() error = nil, want error")
		}
	})
}
