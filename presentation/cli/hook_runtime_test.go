package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_HookSessionCommand_StartRecordsSessionAndState(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	t.Setenv("TRACEARY_PARENT_SESSION_ID", "parent-session")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	storeStub := &storeManagementUsecaseStub{}
	sessionStub := &sessionUsecaseStub{
		startEvent: model.EventOf(
			types.EventID("evt-start"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("claude/planner"),
			types.SessionID("generated-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"session started",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithSession(sessionStub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"cwd":"/tmp/project","agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "start"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := stdout.String(), "generated-session\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !storeStub.initCalled {
		t.Fatal("store Initialize() was not called")
	}
	if got, want := sessionStub.startCall.client, types.Client("hook"); got != want {
		t.Fatalf("start client = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.agent, types.Agent("claude/planner"); got != want {
		t.Fatalf("start agent = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.workspace, types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Fatalf("start workspace = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.parentSessionID, types.SessionID("parent-session"); got != want {
		t.Fatalf("start parentSessionID = %q, want %q", got, want)
	}

	statePath := filepath.Join(homeDir, ".config", "traceary", "hooks", "claude-test-key")
	stateValue, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile(state) error = %v", err)
	}
	if got, want := strings.TrimSpace(string(stateValue)), "generated-session"; got != want {
		t.Fatalf("state session ID = %q, want %q", got, want)
	}
	workspaceStatePath := statePath + "-repo"
	workspaceValue, err := os.ReadFile(workspaceStatePath)
	if err != nil {
		t.Fatalf("ReadFile(workspace state) error = %v", err)
	}
	if got, want := strings.TrimSpace(string(workspaceValue)), "github.com/duck8823/traceary"; got != want {
		t.Fatalf("workspace state = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSessionCommand_StopUsesStateAndCreatesEndMarker(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("claude-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	sessionStub := &sessionUsecaseStub{
		endEvent: model.EventOf(
			types.EventID("evt-end"),
			types.EventKindSessionEnded,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("claude-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"session ended",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "stop"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := sessionStub.endCall.sessionID, types.SessionID("claude-session"); got != want {
		t.Fatalf("end session ID = %q, want %q", got, want)
	}
	if got, want := sessionStub.endCall.workspace, types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Fatalf("end workspace = %q, want %q", got, want)
	}
	if got, want := sessionStub.endCall.agent, types.Agent("claude/planner"); got != want {
		t.Fatalf("end agent = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "claude-test-key")); !os.IsNotExist(err) {
		t.Fatalf("session state still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "claude-test-key-repo")); !os.IsNotExist(err) {
		t.Fatalf("workspace state still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "ended", "claude-claude-session")); err != nil {
		t.Fatalf("Stat(end marker) error = %v", err)
	}
}

func TestRootCLI_HookSessionCommand_StopClearsDuplicateEndStateBeforeStoreInitialization(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(filepath.Join(stateDir, "ended"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("claude-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "ended", "claude-claude-session"), []byte("done"), 0o600); err != nil {
		t.Fatalf("WriteFile(end marker) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{initErr: errors.New("boom")}
	sessionStub := &sessionUsecaseStub{}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "stop"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if storeStub.initCalled {
		t.Fatal("store Initialize() was called for duplicate end cleanup")
	}
	if got := sessionStub.endCall.sessionID; got != "" {
		t.Fatalf("session end call sessionID = %q, want empty for duplicate cleanup", got)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "claude-test-key")); !os.IsNotExist(err) {
		t.Fatalf("session state still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "claude-test-key-repo")); !os.IsNotExist(err) {
		t.Fatalf("workspace state still exists: %v", err)
	}
}

func TestRootCLI_HookSessionCommand_StartClearsStaleWorkspaceStateWhenWorkspaceMissing(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	workspaceStatePath := filepath.Join(stateDir, "claude-test-key-repo")
	if err := os.WriteFile(workspaceStatePath, []byte("stale/workspace"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	sessionStub := &sessionUsecaseStub{
		startEvent: model.EventOf(
			types.EventID("evt-start"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("generated-session"),
			types.Workspace(""),
			"session started",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "start"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(workspaceStatePath); !os.IsNotExist(err) {
		t.Fatalf("workspace state still exists: %v", err)
	}
}

func TestRootCLI_HookAuditCommand_UsesSessionStateAndToolPayload(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-test-key"), []byte("generated-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		auditEvent: model.EventOf(
			types.EventID("evt-audit"),
			types.EventKindCommandExecuted,
			types.Client("hook"),
			types.Agent("codex"),
			types.SessionID("generated-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"command executed",
			time.Now(),
		),
		auditAudit: model.CommandAuditOf(
			types.EventID("evt-audit"),
			"go test ./...",
			`{"command":"go test ./..."}`,
			`{"exitCode":0}`,
			false,
			false,
			types.Some(0),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"tool_input":{"command":"go test ./...","description":"Run tests"},"tool_response":{"exitCode":0,"stdout":"ok","stderr":""}}`))
	rootCmd.SetArgs([]string{"hook", "audit", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := eventStub.auditCall.command, "go test ./..."; got != want {
		t.Fatalf("audit command = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.input, `{"command":"go test ./...","description":"Run tests"}`; got != want {
		t.Fatalf("audit input = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.output, `{"exitCode":0,"stderr":"","stdout":"ok"}`; got != want {
		t.Fatalf("audit output = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.sessionID, types.SessionID("generated-session"); got != want {
		t.Fatalf("audit sessionID = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.workspace, types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Fatalf("audit workspace = %q, want %q", got, want)
	}
	if got, ok := eventStub.auditCall.exitCode.Value(); !ok || got != 0 {
		t.Fatalf("audit exitCode = (%d, %t), want (0, true)", got, ok)
	}
}

func TestRootCLI_HookCompactCommand_SupportsPostCompactAndResume(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("compact-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-compact"),
			types.EventKindCompactSummary,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("compact-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"summary",
			time.Now(),
		),
	}
	contextStub := &contextUsecaseStub{
		handoff: types.Some(apptypes.ContextPackOf(
			types.SessionID("compact-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"",
			"active",
			4,
			1,
			[]string{"claude"},
			apptypes.WorkingStateOf("Investigating flaky tests", "Remember the failing shard"),
			[]string{"go test ./..."},
			nil,
		)),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
		cli.WithContext(contextStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"compact_summary":"Remember the failing shard"}`))
	rootCmd.SetArgs([]string{"hook", "compact", "claude", "post-compact"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(post-compact) error = %v", err)
	}
	if got, want := eventStub.logCall.kind, types.EventKindCompactSummary; got != want {
		t.Fatalf("post-compact log kind = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.message, "Remember the failing shard"; got != want {
		t.Fatalf("post-compact log message = %q, want %q", got, want)
	}

	resumeCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithContext(contextStub),
	).Command()
	stdout := &bytes.Buffer{}
	resumeCmd.SetOut(stdout)
	resumeCmd.SetErr(&bytes.Buffer{})
	resumeCmd.SetIn(strings.NewReader(`{}`))
	resumeCmd.SetArgs([]string{"hook", "compact", "claude", "session-start-compact"})

	if err := resumeCmd.Execute(); err != nil {
		t.Fatalf("Execute(session-start-compact) error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "Session compact-session resumed after compact") {
		t.Fatalf("resume stdout = %q, want compact summary text", got)
	}
}

func TestRootCLI_HookPromptCommand_UsesPromptPayload(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("prompt-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-prompt"),
			types.EventKindPrompt,
			types.Client("hook"),
			types.Agent("claude/planner"),
			types.SessionID("prompt-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"Fix the flaky test",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"prompt":"Fix the flaky test","agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "prompt", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := eventStub.logCall.kind, types.EventKindPrompt; got != want {
		t.Fatalf("prompt log kind = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.message, "Fix the flaky test"; got != want {
		t.Fatalf("prompt log message = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.agent, types.Agent("claude/planner"); got != want {
		t.Fatalf("prompt log agent = %q, want %q", got, want)
	}
}

func TestRootCLI_HookTranscriptCommand_RecordsLastAssistantMessage(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-transcript")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-transcript"), []byte("transcript-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-transcript-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	// Two turns: older assistant message then newer assistant message
	// (plus an unrelated user line). Tool-use blocks must not leak into
	// the captured text.
	transcriptLines := []string{
		`{"role":"user","content":[{"type":"text","text":"initial question"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"older reasoning"},{"type":"tool_use","name":"Read"}]}`,
		`{"role":"user","content":[{"type":"text","text":"follow-up"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"newer reasoning"},{"type":"text","text":"with two blocks"}]}`,
	}
	if err := os.WriteFile(transcriptPath, []byte(strings.Join(transcriptLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(transcript) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-transcript"),
			types.EventKindTranscript,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("transcript-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"newer reasoning\n\nwith two blocks",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	payload := `{"transcript_path":"` + transcriptPath + `"}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "transcript", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
		t.Fatalf("transcript log kind = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.message, "newer reasoning\n\nwith two blocks"; got != want {
		t.Fatalf("transcript log message = %q, want %q", got, want)
	}
}

func TestRootCLI_HookTranscriptCommand_SkipsWhenTranscriptPathMissing(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{}`))
	rootCmd.SetArgs([]string{"hook", "transcript", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// No transcript_path → no event recorded, no failure.
	if eventStub.logCall.message != "" {
		t.Fatalf("logCall.message = %q, want empty when transcript_path missing", eventStub.logCall.message)
	}
}

func TestRootCLI_HookPromptCommand_PrefersPersistedWorkspaceOverEnvOverride(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")
	t.Setenv("TRACEARY_WORKSPACE", "env/workspace")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("prompt-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("state/workspace"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-prompt"),
			types.EventKindPrompt,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("prompt-session"),
			types.Workspace("state/workspace"),
			"Fix the flaky test",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"prompt":"Fix the flaky test"}`))
	rootCmd.SetArgs([]string{"hook", "prompt", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := eventStub.logCall.workspace, types.Workspace("state/workspace"); got != want {
		t.Fatalf("prompt log workspace = %q, want %q", got, want)
	}
}

func TestRootCLI_HookPromptCommand_RecordsCodexPromptPayload(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-test-key"), []byte("codex-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-prompt"),
			types.EventKindPrompt,
			types.Client("hook"),
			types.Agent("codex"),
			types.SessionID("codex-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"Implement feature",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	// Payload mirrors the Codex 0.121.0 UserPromptSubmit schema: all
	// required fields are present, transcript_path is explicitly null, and
	// the runtime should ignore every non-prompt field.
	rootCmd.SetIn(strings.NewReader(`{
		"cwd": "/Users/user/repo",
		"hook_event_name": "UserPromptSubmit",
		"model": "gpt-5.4",
		"permission_mode": "default",
		"prompt": "Implement feature",
		"session_id": "codex-session",
		"transcript_path": null,
		"turn_id": "turn-1"
	}`))
	rootCmd.SetArgs([]string{"hook", "prompt", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := eventStub.logCall.kind, types.EventKindPrompt; got != want {
		t.Fatalf("prompt log kind = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.message, "Implement feature"; got != want {
		t.Fatalf("prompt log message = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.agent, types.Agent("codex"); got != want {
		t.Fatalf("prompt log agent = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.sessionID, types.SessionID("codex-session"); got != want {
		t.Fatalf("prompt log session = %q, want %q", got, want)
	}
}

func TestRootCLI_HookCommand_SwallowsOperationalErrors(t *testing.T) {
	storeStub := &storeManagementUsecaseStub{initErr: errors.New("boom")}
	sessionStub := &sessionUsecaseStub{}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "start"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, want nil for best-effort hook command", err)
	}
	if !storeStub.initCalled {
		t.Fatal("store Initialize() was not called")
	}
}
