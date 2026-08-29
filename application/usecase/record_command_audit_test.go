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

func (s *commandAuditSaverStub) DeleteTranscript(_ context.Context, _ types.EventID) error {
	return nil
}

func TestEventUsecase_Audit(t *testing.T) {
	t.Parallel()

	t.Run("saves audit event successfully", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		wrote, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "go test ./...",
				Input:     "stdin",
				Output:    "stdout",
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace("duck8823/traceary"),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		event := wrote.Event()
		if event == nil || commandAudit == nil {
			t.Fatalf("Audit() returned nil values")
		}
		if stub.savedEvent != event {
			t.Fatalf("saved event mismatch")
		}
		if stub.savedCommandAudit != commandAudit {
			t.Fatalf("saved command audit mismatch")
		}
		if commandAudit.InputOriginalBytes() != 0 || commandAudit.OutputOriginalBytes() != 0 {
			t.Fatalf("original bytes = (%d, %d), want zero for untruncated payloads", commandAudit.InputOriginalBytes(), commandAudit.OutputOriginalBytes())
		}
		if diff := cmp.Diff("command_executed", event.Kind().String()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
		// command_executed keeps the execution record in command_audits only.
		if diff := cmp.Diff("", event.Body()); diff != "" {
			t.Fatalf("Body() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("go test ./...", commandAudit.Command()); diff != "" {
			t.Fatalf("Command() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("stdin", commandAudit.Input()); diff != "" {
			t.Fatalf("Input() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("stdout", commandAudit.Output()); diff != "" {
			t.Fatalf("Output() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("redacts command secret shapes before saving", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "API_KEY=env-secret curl --token flag-secret https://example.test?access_token=query-secret",
				Input:     "stdin",
				Output:    "stdout",
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		for _, leaked := range []string{"env-secret", "flag-secret", "query-secret"} {
			if strings.Contains(commandAudit.Command(), leaked) || strings.Contains(commandAudit.Input(), leaked) || strings.Contains(commandAudit.Output(), leaked) {
				t.Fatalf("command secret %q leaked: command=%q input=%q output=%q", leaked, commandAudit.Command(), commandAudit.Input(), commandAudit.Output())
			}
		}
		for _, want := range []string{"API_KEY=[REDACTED]", "--token [REDACTED]", "access_token=%5BREDACTED%5D"} {
			if !strings.Contains(commandAudit.Command(), want) {
				t.Fatalf("Command() = %q, want %q", commandAudit.Command(), want)
			}
		}
	})

	t.Run("truncates long input/output before saving", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)
		longInput := "input-head-" + strings.Repeat("i", 70*1024) + "-input-tail"
		longOutput := "output-head-" + strings.Repeat("o", 70*1024) + "-output-tail"

		wrote, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "go test ./...",
				Input:     longInput,
				Output:    longOutput,
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		event := wrote.Event()
		if !commandAudit.InputTruncated() {
			t.Fatalf("InputTruncated() = false, want true")
		}
		if !commandAudit.OutputTruncated() {
			t.Fatalf("OutputTruncated() = false, want true")
		}
		if commandAudit.InputOriginalBytes() != len(longInput) {
			t.Fatalf("InputOriginalBytes() = %d, want %d", commandAudit.InputOriginalBytes(), len(longInput))
		}
		if commandAudit.OutputOriginalBytes() != len(longOutput) {
			t.Fatalf("OutputOriginalBytes() = %d, want %d", commandAudit.OutputOriginalBytes(), len(longOutput))
		}
		for _, want := range []string{"input-head-", "-input-tail", "truncated original_bytes="} {
			if !strings.Contains(commandAudit.Input(), want) {
				t.Fatalf("Input() missing %q in truncated head/tail payload", want)
			}
		}
		for _, want := range []string{"output-head-", "-output-tail", "truncated original_bytes="} {
			if !strings.Contains(commandAudit.Output(), want) {
				t.Fatalf("Output() missing %q in truncated head/tail payload", want)
			}
		}
		// Truncation markers live on the audit payloads, not a composed body.
		if !commandAudit.InputTruncated() || !commandAudit.OutputTruncated() {
			t.Fatalf("expected truncated audit payloads after long input/output")
		}
		if !strings.Contains(commandAudit.Input(), "truncated original_bytes=") {
			t.Fatalf("Input() missing truncation metadata: %s", commandAudit.Input())
		}
		if !strings.Contains(commandAudit.Output(), "truncated original_bytes=") {
			t.Fatalf("Output() missing truncation metadata: %s", commandAudit.Output())
		}
		if diff := cmp.Diff("", event.Body()); diff != "" {
			t.Fatalf("Body() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("truncates input/output at explicit limit", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "go test ./...",
				Input:     strings.Repeat("i", 32),
				Output:    strings.Repeat("o", 32),
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
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
			apptypes.AuditInput{
				Command:   "curl https://example.test",
				Input:     `{"access_token":"top-secret","note":"keep"}`,
				Output:    "Authorization: Bearer token-value\nexport API_KEY=\"abc123\"",
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
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
			apptypes.AuditInput{
				Command:   "curl https://example.test",
				Input:     `{"access_token":"top-secret"}`,
				Output:    "Authorization: Bearer token-value",
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
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
			apptypes.AuditInput{
				Command:   "curl https://example.test",
				Input:     "my_custom_secret=hunter2",
				Output:    "internal_token: abc123",
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
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
			apptypes.AuditInput{
				Command:   "test",
				Input:     "",
				Output:    "",
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
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
			apptypes.AuditInput{
				Command:   "go test ./...",
				Input:     "stdin",
				Output:    "stdout",
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				ExitCode:  types.None[int](),
				Failed:    false,
			},
			apptypes.NewAuditRedactionBuilder().
				MaxInputBytes(-1).
				Build(),
		)
		if err == nil {
			t.Fatalf("Audit() error = nil, want error")
		}
	})

	t.Run("read-only tool stores empty output with metadata", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)
		output := "file contents that must not be stored"
		_, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "Read",
				Input:     `{"file_path":"README.md"}`,
				Output:    output,
				Client:    types.Client("hook"),
				Agent:     types.Agent("claude"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace("duck8823/traceary"),
				ExitCode:  types.None[int](),
				Host:      types.Client("claude"),
				ToolName:  "Read",
				ToolInput: map[string]any{"file_path": "README.md"},
			},
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if diff := cmp.Diff("", commandAudit.Output()); diff != "" {
			t.Fatalf("Output() mismatch (-want +got):\n%s", diff)
		}
		if commandAudit.OutputTruncated() {
			t.Fatalf("OutputTruncated() = true, want false")
		}
		if commandAudit.OutputOriginalBytes() != 0 {
			t.Fatalf("OutputOriginalBytes() = %d, want 0", commandAudit.OutputOriginalBytes())
		}
		metadata, ok := commandAudit.OutputMetadata().Value()
		if !ok {
			t.Fatal("OutputMetadata() is None, want metadata-only capture")
		}
		want := types.ReadOnlyOutputMetadataOf([]string{"README.md"}, output, 64*1024)
		if diff := cmp.Diff(want.SHA256(), metadata.SHA256()); diff != "" {
			t.Fatalf("SHA256 mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want.Paths(), metadata.Paths()); diff != "" {
			t.Fatalf("Paths mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(len(output), metadata.Bytes()); diff != "" {
			t.Fatalf("Bytes mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("read-only tool marks truncated above the cap without truncating text", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)
		output := strings.Repeat("o", 40)
		_, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "Read",
				Input:     `{"file_path":"README.md"}`,
				Output:    output,
				Client:    types.Client("hook"),
				Agent:     types.Agent("claude"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				Host:      types.Client("claude"),
				ToolName:  "Read",
				ToolInput: map[string]any{"file_path": "README.md"},
			},
			apptypes.NewAuditRedactionBuilder().MaxOutputBytes(20).Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if diff := cmp.Diff("", commandAudit.Output()); diff != "" {
			t.Fatalf("Output() mismatch (-want +got):\n%s", diff)
		}
		if commandAudit.OutputTruncated() {
			t.Fatalf("row OutputTruncated() = true, want false")
		}
		metadata, ok := commandAudit.OutputMetadata().Value()
		if !ok {
			t.Fatal("OutputMetadata() is None")
		}
		if !metadata.Truncated() {
			t.Fatalf("metadata Truncated() = false, want true")
		}
		if diff := cmp.Diff(len(output), metadata.Bytes()); diff != "" {
			t.Fatalf("Bytes mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("read-only tool hashes the redacted output", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)
		secretOutput := "Authorization: Bearer token-value"
		_, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "Read",
				Input:     `{"file_path":".env"}`,
				Output:    secretOutput,
				Client:    types.Client("hook"),
				Agent:     types.Agent("claude"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				Host:      types.Client("claude"),
				ToolName:  "Read",
				ToolInput: map[string]any{"file_path": ".env"},
			},
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if strings.Contains(commandAudit.Output(), "token-value") {
			t.Fatalf("Output() leaked secret: %q", commandAudit.Output())
		}
		metadata, ok := commandAudit.OutputMetadata().Value()
		if !ok {
			t.Fatal("OutputMetadata() is None")
		}
		rawDigest := types.ReadOnlyOutputMetadataOf(nil, secretOutput, 64*1024).SHA256()
		if metadata.SHA256() == rawDigest {
			t.Fatalf("SHA256 hashed the unredacted secret")
		}
		if strings.Contains(metadata.SHA256(), "token-value") {
			t.Fatalf("metadata digest leaked secret text")
		}
	})

	t.Run("mutating tool keeps full capture", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)
		_, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "Bash",
				Input:     `{"command":"echo hi"}`,
				Output:    "hi\n",
				Client:    types.Client("hook"),
				Agent:     types.Agent("claude"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
				Host:      types.Client("claude"),
				ToolName:  "Bash",
				ToolInput: map[string]any{"command": "echo hi"},
			},
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if diff := cmp.Diff("hi\n", commandAudit.Output()); diff != "" {
			t.Fatalf("Output() mismatch (-want +got):\n%s", diff)
		}
		if _, ok := commandAudit.OutputMetadata().Value(); ok {
			t.Fatal("OutputMetadata() set for a mutating tool")
		}
	})

	t.Run("failed read-only tool keeps full capture", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)
		_, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:       "Read",
				Input:         `{"file_path":"secret.md"}`,
				Output:        "Permission denied",
				Client:        types.Client("hook"),
				Agent:         types.Agent("claude"),
				SessionID:     types.SessionID("session-1"),
				Workspace:     types.Workspace(""),
				Failed:        true,
				FailureReason: types.CommandFailureReasonHookDenied,
				Host:          types.Client("claude"),
				ToolName:      "Read",
				ToolInput:     map[string]any{"file_path": "secret.md"},
			},
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if diff := cmp.Diff("Permission denied", commandAudit.Output()); diff != "" {
			t.Fatalf("Output() mismatch (-want +got):\n%s", diff)
		}
		if _, ok := commandAudit.OutputMetadata().Value(); ok {
			t.Fatal("OutputMetadata() set for a failed read-only tool")
		}
	})

	t.Run("manual audit without host keeps full capture", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewEventUsecase(stub, nil)
		_, commandAudit, err := sut.Audit(context.Background(),
			apptypes.AuditInput{
				Command:   "Read",
				Input:     "stdin",
				Output:    "stdout",
				Client:    types.Client("cli"),
				Agent:     types.Agent("codex"),
				SessionID: types.SessionID("session-1"),
				Workspace: types.Workspace(""),
			},
			apptypes.NewAuditRedactionBuilder().Build(),
		)
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if diff := cmp.Diff("stdout", commandAudit.Output()); diff != "" {
			t.Fatalf("Output() mismatch (-want +got):\n%s", diff)
		}
		if _, ok := commandAudit.OutputMetadata().Value(); ok {
			t.Fatal("OutputMetadata() set for a manual audit")
		}
	})
}

func TestCommandAuditDeliveryFields_DistinguishesOutputMetadata(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	left, err := model.NewCommandAudit(eventID, "Read", `{"file_path":"a.md"}`, "", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit(left) error = %v", err)
	}
	right, err := model.NewCommandAudit(eventID, "Read", `{"file_path":"b.md"}`, "", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit(right) error = %v", err)
	}
	sameBytes := "12345"
	left.SetReadOnlyOutputMetadata(types.ReadOnlyOutputMetadataOf([]string{"a.md"}, sameBytes, 64))
	right.SetReadOnlyOutputMetadata(types.ReadOnlyOutputMetadataOf([]string{"b.md"}, sameBytes, 64))

	leftFields := usecase.CommandAuditDeliveryFieldsForTest(left)
	rightFields := usecase.CommandAuditDeliveryFieldsForTest(right)
	if cmp.Equal(leftFields, rightFields) {
		t.Fatalf("delivery fields collided for two reads of equal byte count: %v", leftFields)
	}
}
