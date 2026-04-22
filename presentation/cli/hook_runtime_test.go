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

func TestRootCLI_HookCompactCommand_RecordsPreCompactSnapshot(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("pre-compact-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-pre-compact"),
			types.EventKindCompactSummary,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("pre-compact-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"trigger":"size-threshold"}`))
	rootCmd.SetArgs([]string{"hook", "compact", "claude", "pre-compact"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(pre-compact) error = %v", err)
	}
	if got, want := eventStub.logCall.kind, types.EventKindCompactSummary; got != want {
		t.Fatalf("pre-compact log kind = %q, want %q", got, want)
	}
	if !strings.HasPrefix(eventStub.logCall.message, types.EventBodyMarkerCompactPreSnapshot) {
		t.Fatalf("pre-compact log message = %q, want prefix %q", eventStub.logCall.message, types.EventBodyMarkerCompactPreSnapshot)
	}
	if !strings.Contains(eventStub.logCall.message, "size-threshold") {
		t.Fatalf("pre-compact log message = %q, want trigger context appended", eventStub.logCall.message)
	}
}

func TestRootCLI_HookSubagentStopCommand_RecordsSessionEnded(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("subagent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-subagent"),
			types.EventKindSessionEnded,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("subagent-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"subagent_type":"code-reviewer"}`))
	rootCmd.SetArgs([]string{"hook", "subagent-stop", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-stop) error = %v", err)
	}
	if got, want := eventStub.logCall.kind, types.EventKindSessionEnded; got != want {
		t.Fatalf("subagent-stop log kind = %q, want %q", got, want)
	}
	if !strings.HasPrefix(eventStub.logCall.message, types.EventBodyMarkerSubagentStop) {
		t.Fatalf("subagent-stop log message = %q, want prefix %q", eventStub.logCall.message, types.EventBodyMarkerSubagentStop)
	}
	if !strings.Contains(eventStub.logCall.message, "code-reviewer") {
		t.Fatalf("subagent-stop log message = %q, want subagent_type appended", eventStub.logCall.message)
	}
}

func TestRootCLI_HookSubagentStopCommand_NoOpWhenSessionIDMissing(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-missing")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	eventStub := &eventUsecaseStub{}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{}`))
	rootCmd.SetArgs([]string{"hook", "subagent-stop", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-stop) error = %v", err)
	}
	if eventStub.logCall.kind != types.EventKind("") {
		t.Fatalf("subagent-stop should not call Log when session ID is unresolvable; got kind=%q", eventStub.logCall.kind)
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
	// Real Claude Code JSONL envelope: top-level `type=assistant` with
	// nested `message.role` / `message.content`. We also drop an
	// envelope-only snapshot line, a tool_use block (captured by
	// command_executed audits), and interleave a `thinking` block on
	// the last turn to verify extended-thinking is included.
	transcriptLines := []string{
		`{"type":"file-history-snapshot","messageId":"x","snapshot":{},"isSnapshotUpdate":false}`,
		`{"type":"user","message":{"role":"user","content":"initial question"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"older reasoning"},{"type":"tool_use","name":"Read"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"ok"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"first-block thinking","signature":"sig"},{"type":"text","text":"newer reasoning"},{"type":"text","text":"with two blocks"}]}}`,
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
			"first-block thinking\n\nnewer reasoning\n\nwith two blocks",
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
	if got, want := eventStub.logCall.message, "first-block thinking\n\nnewer reasoning\n\nwith two blocks"; got != want {
		t.Fatalf("transcript log message = %q, want %q", got, want)
	}
}

// TestRootCLI_HookTranscriptCommand_AppliesExtraRedactPatterns asserts
// that operator-configured extra_redact_patterns also run against the
// transcript body, matching the audit path's policy. Before #626 the
// transcript hook used ApplyBuiltin only and silently leaked any
// org-specific secret shape that the audit path successfully masked.
func TestRootCLI_HookTranscriptCommand_AppliesExtraRedactPatterns(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-transcript-extra")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-transcript-extra"), []byte("transcript-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-transcript-extra-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	// Reasoning body contains a secret shape that the built-in redactors
	// do NOT match, but an operator-supplied extra pattern does.
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Org token: my_custom_secret=s3cr3tValue42 is now stale."}]}}`
	if err := os.WriteFile(transcriptPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(transcript) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-transcript-extra"),
			types.EventKindTranscript,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("transcript-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"ignored-in-test-eventstub",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
		cli.WithExtraRedactPatterns([]string{`my_custom_secret=\S+`}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	payload := `{"transcript_path":"` + transcriptPath + `"}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "transcript", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if strings.Contains(eventStub.logCall.message, "s3cr3tValue42") {
		t.Errorf("transcript log message leaked secret via extra pattern: %q", eventStub.logCall.message)
	}
	if !strings.Contains(eventStub.logCall.message, "[REDACTED]") {
		t.Errorf("transcript log message missing [REDACTED] placeholder: %q", eventStub.logCall.message)
	}
}

// TestRootCLI_HookTranscriptCommand_HandlesMalformedTranscript asserts
// that lines which fail to parse are skipped rather than aborting the
// whole Stop hook. Structural drift in the JSONL format (e.g. future
// Claude Code revisions adding a new envelope type) must not mask the
// last good assistant message when one is present.
func TestRootCLI_HookTranscriptCommand_HandlesMalformedTranscript(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-transcript-mal")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-transcript-mal"), []byte("mal-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-transcript-mal-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	transcriptPath := filepath.Join(t.TempDir(), "mixed.jsonl")
	transcriptLines := []string{
		`{not-json`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"valid assistant reply"}]}}`,
		`{"type":"user","message":{"role":"user","content":"noise"}}`,
	}
	if err := os.WriteFile(transcriptPath, []byte(strings.Join(transcriptLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(transcript) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-mal"),
			types.EventKindTranscript,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("mal-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"valid assistant reply",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"transcript_path":"` + transcriptPath + `"}`))
	rootCmd.SetArgs([]string{"hook", "transcript", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := eventStub.logCall.message, "valid assistant reply"; got != want {
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

// TestRootCLI_HookTranscriptCommand_Codex asserts that the Codex Stop
// hook records the `last_assistant_message` payload verbatim with
// extra_redact_patterns applied. Codex delivers the final turn inline
// — there is no JSONL file to read — so the extractor must pull the
// field straight out of the Stop-hook stdin payload.
func TestRootCLI_HookTranscriptCommand_Codex(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-transcript-codex")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-test-key-transcript-codex"), []byte("codex-transcript-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-test-key-transcript-codex-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-transcript-codex"),
			types.EventKindTranscript,
			types.Client("hook"),
			types.Agent("codex"),
			types.SessionID("codex-transcript-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"ignored-in-test-eventstub",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
		cli.WithExtraRedactPatterns([]string{`my_custom_secret=\S+`}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	payload := `{"last_assistant_message":"Codex reply body. Org leak: my_custom_secret=s3cr3tValue42 follows.","session_id":"codex-transcript-session"}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "transcript", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
		t.Fatalf("transcript log kind = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.agent, types.Agent("codex"); got != want {
		t.Fatalf("transcript log agent = %q, want %q", got, want)
	}
	if !strings.HasPrefix(eventStub.logCall.message, "Codex reply body.") {
		t.Errorf("transcript log message prefix = %q, want it to start with \"Codex reply body.\"", eventStub.logCall.message)
	}
	if strings.Contains(eventStub.logCall.message, "s3cr3tValue42") {
		t.Errorf("transcript log message leaked secret via extra pattern: %q", eventStub.logCall.message)
	}
	if !strings.Contains(eventStub.logCall.message, "[REDACTED]") {
		t.Errorf("transcript log message missing [REDACTED] placeholder: %q", eventStub.logCall.message)
	}
}

// TestRootCLI_HookTranscriptCommand_CodexSkipsWhenMessageEmpty asserts
// that a missing or blank `last_assistant_message` field produces no
// event and no error. This preserves the fail-soft contract shared
// with the Claude path.
func TestRootCLI_HookTranscriptCommand_CodexSkipsWhenMessageEmpty(t *testing.T) {
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
	rootCmd.SetIn(strings.NewReader(`{"last_assistant_message":"   "}`))
	rootCmd.SetArgs([]string{"hook", "transcript", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if eventStub.logCall.message != "" {
		t.Fatalf("logCall.message = %q, want empty when last_assistant_message is blank", eventStub.logCall.message)
	}
}

// TestRootCLI_HookTranscriptCommand_Gemini asserts that the Gemini
// AfterAgent hook records the `prompt_response` payload with
// redaction applied. Gemini has no Stop event, so transcript capture
// is attached to AfterAgent instead.
func TestRootCLI_HookTranscriptCommand_Gemini(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-transcript-gemini")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "gemini-test-key-transcript-gemini"), []byte("gemini-transcript-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "gemini-test-key-transcript-gemini-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-transcript-gemini"),
			types.EventKindTranscript,
			types.Client("hook"),
			types.Agent("gemini"),
			types.SessionID("gemini-transcript-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"ignored-in-test-eventstub",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
		cli.WithExtraRedactPatterns([]string{`my_custom_secret=\S+`}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	payload := `{"prompt_response":"Gemini summary. Org leak: my_custom_secret=s3cr3tValue42 trailing.","session_id":"gemini-transcript-session"}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "transcript", "gemini"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
		t.Fatalf("transcript log kind = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.agent, types.Agent("gemini"); got != want {
		t.Fatalf("transcript log agent = %q, want %q", got, want)
	}
	if !strings.HasPrefix(eventStub.logCall.message, "Gemini summary.") {
		t.Errorf("transcript log message prefix = %q, want it to start with \"Gemini summary.\"", eventStub.logCall.message)
	}
	if strings.Contains(eventStub.logCall.message, "s3cr3tValue42") {
		t.Errorf("transcript log message leaked secret via extra pattern: %q", eventStub.logCall.message)
	}
	if !strings.Contains(eventStub.logCall.message, "[REDACTED]") {
		t.Errorf("transcript log message missing [REDACTED] placeholder: %q", eventStub.logCall.message)
	}
}

// TestRootCLI_HookTranscriptCommand_GeminiSkipsWhenPromptResponseEmpty
// mirrors the Claude / Codex empty-body contract for Gemini's
// AfterAgent payload shape.
func TestRootCLI_HookTranscriptCommand_GeminiSkipsWhenPromptResponseEmpty(t *testing.T) {
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
	rootCmd.SetIn(strings.NewReader(`{"prompt_response":""}`))
	rootCmd.SetArgs([]string{"hook", "transcript", "gemini"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if eventStub.logCall.message != "" {
		t.Fatalf("logCall.message = %q, want empty when prompt_response is blank", eventStub.logCall.message)
	}
}

// TestRootCLI_HookTranscriptCommand_UnknownClient asserts that a
// transcript hook fired with an unknown client argument silently skips
// instead of aborting the host's Stop / SessionEnd hook. Forward
// compatibility: a packaged hook may arrive before its extractor is
// registered during staged rollouts.
func TestRootCLI_HookTranscriptCommand_UnknownClient(t *testing.T) {
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
	rootCmd.SetIn(strings.NewReader(`{"last_assistant_message":"would-be-text"}`))
	rootCmd.SetArgs([]string{"hook", "transcript", "future-host"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if eventStub.logCall.message != "" {
		t.Fatalf("logCall.message = %q, want empty for unknown client", eventStub.logCall.message)
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
