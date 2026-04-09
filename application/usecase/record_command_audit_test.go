package usecase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

type commandAuditSaverStub struct {
	receivedPath      string
	savedEvent        *model.Event
	savedCommandAudit *model.CommandAudit
	err               error
}

func (s *commandAuditSaverStub) SaveCommandAudit(
	_ context.Context,
	dbPath string,
	event *model.Event,
	commandAudit *model.CommandAudit,
) error {
	s.receivedPath = dbPath
	s.savedEvent = event
	s.savedCommandAudit = commandAudit
	return s.err
}

func TestRecordCommandAuditUsecase_Run(t *testing.T) {
	t.Parallel()

	t.Run("監査イベントを保存できる", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)

		event, commandAudit, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:    "/tmp/traceary.db",
			Command:   "go test ./...",
			Input:     "stdin",
			Output:    "stdout",
			Client:    "cli",
			Agent:     "codex",
			SessionID: "session-1",
			Repo:      "duck8823/traceary",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if event == nil || commandAudit == nil {
			t.Fatalf("Run() returned nil values")
		}
		if stub.receivedPath != "/tmp/traceary.db" {
			t.Fatalf("received path = %q, want %q", stub.receivedPath, "/tmp/traceary.db")
		}
		if stub.savedEvent != event {
			t.Fatalf("saved event mismatch")
		}
		if stub.savedCommandAudit != commandAudit {
			t.Fatalf("saved command audit mismatch")
		}
		if event.Kind().String() != "command_executed" {
			t.Fatalf("Kind() = %q, want %q", event.Kind(), "command_executed")
		}
		if event.Body() != "go test ./..." {
			t.Fatalf("Body() = %q, want %q", event.Body(), "go test ./...")
		}
	})

	t.Run("長い input/output は切り詰めて保存する", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)
		longInput := strings.Repeat("i", 70*1024)
		longOutput := strings.Repeat("o", 70*1024)

		_, commandAudit, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:    "/tmp/traceary.db",
			Command:   "go test ./...",
			Input:     longInput,
			Output:    longOutput,
			Client:    "cli",
			Agent:     "codex",
			SessionID: "session-1",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
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

	t.Run("明示した上限で input/output を切り詰める", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)

		_, commandAudit, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:         "/tmp/traceary.db",
			Command:        "go test ./...",
			Input:          strings.Repeat("i", 32),
			Output:         strings.Repeat("o", 32),
			Client:         "cli",
			Agent:          "codex",
			SessionID:      "session-1",
			MaxInputBytes:  16,
			MaxOutputBytes: 20,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if len(commandAudit.Input()) != 16 {
			t.Fatalf("len(Input()) = %d, want 16", len(commandAudit.Input()))
		}
		if len(commandAudit.Output()) != 20 {
			t.Fatalf("len(Output()) = %d, want 20", len(commandAudit.Output()))
		}
		if !commandAudit.InputTruncated() || !commandAudit.OutputTruncated() {
			t.Fatalf("truncated flags = (%t, %t), want both true", commandAudit.InputTruncated(), commandAudit.OutputTruncated())
		}
	})

	t.Run("一般的な secret は既定で伏せ字にする", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)

		_, commandAudit, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:    "/tmp/traceary.db",
			Command:   "curl https://example.test",
			Input:     `{"access_token":"top-secret","note":"keep"}`,
			Output:    "Authorization: Bearer token-value\nexport API_KEY=\"abc123\"",
			Client:    "cli",
			Agent:     "codex",
			SessionID: "session-1",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
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

	t.Run("allow secrets が有効なら raw payload を保存する", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)

		_, commandAudit, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:        "/tmp/traceary.db",
			Command:       "curl https://example.test",
			Input:         `{"access_token":"top-secret"}`,
			Output:        "Authorization: Bearer token-value",
			Client:        "cli",
			Agent:         "codex",
			SessionID:     "session-1",
			AllowSecrets:  true,
			MaxInputBytes: 256,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
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

	t.Run("追加リダクションパターンでカスタムフィールドを伏せ字にする", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)

		_, commandAudit, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:              "/tmp/traceary.db",
			Command:             "curl https://example.test",
			Input:               "my_custom_secret=hunter2",
			Output:              "internal_token: abc123",
			Client:              "cli",
			Agent:               "codex",
			SessionID:           "session-1",
			ExtraRedactPatterns: []string{"my_custom_secret=\\S+", "internal_token:\\s*\\S+"},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
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

	t.Run("不正な追加リダクションパターンはエラーを返す", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)

		_, _, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:              "/tmp/traceary.db",
			Command:             "test",
			Input:               "",
			Output:              "",
			Client:              "cli",
			Agent:               "codex",
			SessionID:           "session-1",
			ExtraRedactPatterns: []string{"[invalid"},
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error for invalid regex")
		}
	})

	t.Run("負の上限はエラー", func(t *testing.T) {
		t.Parallel()

		stub := &commandAuditSaverStub{}
		sut := usecase.NewRecordCommandAuditUsecase(stub)

		_, _, err := sut.Run(context.Background(), usecase.RecordCommandAuditInput{
			DBPath:        "/tmp/traceary.db",
			Command:       "go test ./...",
			Input:         "stdin",
			Output:        "stdout",
			Client:        "cli",
			Agent:         "codex",
			SessionID:     "session-1",
			MaxInputBytes: -1,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
