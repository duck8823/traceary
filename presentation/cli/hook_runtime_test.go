package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
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

func TestRootCLI_HookSessionCommand_StartInfersParentFromActiveSubagentState(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "infer-key")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	activeDir := filepath.Join(homeDir, ".config", "traceary", "hooks", "active-subagents")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(activeDir) error = %v", err)
	}
	activeJSON := `{"children":{"toolu_child":{"child_session_id":"parent-session:sub:toolu_child","started_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}}}`
	if err := os.WriteFile(filepath.Join(activeDir, "claude-parent-session"), []byte(activeJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(active state) error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{
		activeEvent: model.EventOf(
			types.EventID("evt-parent-start"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("parent-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"session started",
			time.Now(),
		),
		startEvent: model.EventOf(
			types.EventID("evt-child-start"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("claude/planner"),
			types.SessionID("child-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"session started",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"child-session","cwd":"/tmp/project","agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "start"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := sessionStub.startCall.parentSessionID, types.SessionID("parent-session"); got != want {
		t.Fatalf("inferred parentSessionID = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSessionCommand_StartInfersParentWhenActiveSessionIsSyntheticChild(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "infer-synthetic-key")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	activeDir := filepath.Join(homeDir, ".config", "traceary", "hooks", "active-subagents")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(activeDir) error = %v", err)
	}
	activeJSON := `{"children":{"toolu_child":{"child_session_id":"parent-session:sub:toolu_child","started_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}}}`
	if err := os.WriteFile(filepath.Join(activeDir, "claude-parent-session"), []byte(activeJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(active state) error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{
		activeEvent: model.EventOf(
			types.EventID("evt-child-boundary"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("claude/worker"),
			types.SessionID("parent-session:sub:toolu_child"),
			types.Workspace("github.com/duck8823/traceary"),
			"session started",
			time.Now(),
		),
		startEvent: model.EventOf(
			types.EventID("evt-subagent-session-start"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("claude/worker"),
			types.SessionID("real-subagent-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"session started",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"real-subagent-session","cwd":"/tmp/project","agent_type":"worker"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "start"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := sessionStub.startCall.parentSessionID, types.SessionID("parent-session"); got != want {
		t.Fatalf("inferred parentSessionID = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSessionCommand_ExplicitParentOverridesInference(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "explicit-infer-key")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	t.Setenv("TRACEARY_PARENT_SESSION_ID", "explicit-parent")

	sessionStub := &sessionUsecaseStub{
		activeEvent: model.EventOf(types.EventID("evt-parent-start"), types.EventKindSessionStarted, types.Client("hook"), types.Agent("claude"), types.SessionID("inferred-parent"), types.Workspace("github.com/duck8823/traceary"), "session started", time.Now()),
		startEvent:  model.EventOf(types.EventID("evt-child-start"), types.EventKindSessionStarted, types.Client("hook"), types.Agent("claude/planner"), types.SessionID("child-session"), types.Workspace("github.com/duck8823/traceary"), "session started", time.Now()),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"child-session","agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "start"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := sessionStub.startCall.parentSessionID, types.SessionID("explicit-parent"); got != want {
		t.Fatalf("parentSessionID = %q, want explicit %q", got, want)
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

func TestRootCLI_HookSessionCommand_StopFiresMemoryAutoExtract(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-extract")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-extract"), []byte("auto-extract-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-extract-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	memoryStub := &memoryUsecaseStub{}
	sessionStub := &sessionUsecaseStub{
		endEvent: model.EventOf(
			types.EventID("evt-end-extract"),
			types.EventKindSessionEnded,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("auto-extract-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"session ended",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithMemory(memoryStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "stop"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := memoryStub.extractCriteria.SessionID(), types.SessionID("auto-extract-session"); got != want {
		t.Fatalf("auto-extract session ID = %q, want %q", got, want)
	}
	if got, want := memoryStub.extractCriteria.Workspace(), types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Fatalf("auto-extract workspace = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSessionCommand_StopAutoExtractFailureDoesNotPropagate(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-extract-fail")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-extract-fail"), []byte("auto-extract-fail-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-extract-fail-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	memoryStub := &memoryUsecaseStub{
		extractErr: errors.New("simulated extract failure"),
	}
	sessionStub := &sessionUsecaseStub{
		endEvent: model.EventOf(
			types.EventID("evt-end-extract-fail"),
			types.EventKindSessionEnded,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("auto-extract-fail-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"session ended",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithMemory(memoryStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "stop"})

	// runHookBestEffort swallows the error in production, but the test
	// runtime returns nil too — auto-extract failure must NEVER block
	// the session-end record from committing.
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v (auto-extract failure must not propagate)", err)
	}
	if got, want := sessionStub.endCall.sessionID, types.SessionID("auto-extract-fail-session"); got != want {
		t.Fatalf("session.End sessionID = %q, want %q (must be invoked even when extract fails)", got, want)
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
			false,
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
	if eventStub.auditCall.failed {
		t.Fatalf("audit failed = true, want false for a success payload")
	}
}

func TestRootCLI_HookAuditCommand_PrefersToolCWDWorkspaceOverSessionState(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "workspace-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-workspace-key"), []byte("generated-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-workspace-key-repo"), []byte("github.com/duck8823/calorie-balance"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	repoDir := initGitRepoForHookRuntimeTest(t)
	runGitCommandForHookRuntimeTest(t, repoDir, "remote", "add", "origin", "git@github.com:duck8823/dotfiles.git")

	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"cwd":` + quoteJSONStringForHookRuntimeTest(repoDir) + `,"tool_input":{"command":"git status"},"tool_response":{"stdout":"ok"}}`))
	rootCmd.SetArgs([]string{"hook", "audit", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := eventStub.auditCall.workspace, types.Workspace("github.com/duck8823/dotfiles"); got != want {
		t.Fatalf("audit workspace = %q, want %q", got, want)
	}
}

// TestRootCLI_HookAuditCommand_FlagsFailureFromErrorPayload verifies the
// failure-derivation logic: no host exposes a numeric exit code in the
// post-tool payload, so a Claude PostToolUseFailure (top-level "error") and a
// Gemini spawn error (nested "tool_response.error") must each be recorded as a
// first-class failure (failed=true) even though exitCode stays unset.
func TestRootCLI_HookAuditCommand_FlagsFailureFromErrorPayload(t *testing.T) {
	cases := []struct {
		name       string
		client     string
		payload    string
		wantFailed bool
	}{
		{
			name:       "claude post-tool-use failure with top-level error",
			client:     "claude",
			payload:    `{"tool_input":{"command":"go test ./..."},"error":"Exit code 7\nFAIL","is_interrupt":false}`,
			wantFailed: true,
		},
		{
			name:       "claude success payload is not flagged",
			client:     "claude",
			payload:    `{"tool_input":{"command":"go test ./..."},"tool_response":{"interrupted":false,"stdout":"ok","stderr":""}}`,
			wantFailed: false,
		},
		{
			name:       "gemini spawn error in tool_response.error",
			client:     "gemini",
			payload:    `{"tool_input":{"command":"missing-binary"},"tool_response":{"llmContent":"failed","error":{"type":"shell_execute_error","message":"spawn failed"}}}`,
			wantFailed: true,
		},
		{
			name:       "empty top-level error is not a failure",
			client:     "claude",
			payload:    `{"tool_input":{"command":"go test ./..."},"error":""}`,
			wantFailed: false,
		},
		{
			name:       "gemini empty tool_response.error is not a failure",
			client:     "gemini",
			payload:    `{"tool_input":{"command":"go test ./..."},"tool_response":{"llmContent":"ok","error":""}}`,
			wantFailed: false,
		},
		{
			name:       "codex raw-string tool_response mentioning error is not flagged",
			client:     "codex",
			payload:    `{"tool_input":{"command":"go test ./..."},"tool_response":"error: a test logged the word error but exited 0"}`,
			wantFailed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

			homeDir := t.TempDir()
			cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
			t.Cleanup(cli.ResetUserHomeDirFunc)

			stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
			if err := os.MkdirAll(stateDir, 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(stateDir, tc.client+"-test-key"), []byte("generated-session"), 0o600); err != nil {
				t.Fatalf("WriteFile(session state) error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(stateDir, tc.client+"-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
				t.Fatalf("WriteFile(workspace state) error = %v", err)
			}

			eventStub := &eventUsecaseStub{
				auditEvent: model.EventOf(
					types.EventID("evt-audit"),
					types.EventKindCommandExecuted,
					types.Client("hook"),
					types.Agent(tc.client),
					types.SessionID("generated-session"),
					types.Workspace("github.com/duck8823/traceary"),
					"command executed",
					time.Now(),
				),
			}

			rootCmd := newTestRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithEvent(eventStub),
			).Command()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetIn(strings.NewReader(tc.payload))
			rootCmd.SetArgs([]string{"hook", "audit", tc.client})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if got := eventStub.auditCall.failed; got != tc.wantFailed {
				t.Fatalf("audit failed = %t, want %t", got, tc.wantFailed)
			}
		})
	}
}

func initGitRepoForHookRuntimeTest(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runGitCommandForHookRuntimeTest(t, repoDir, "init")
	return repoDir
}

func runGitCommandForHookRuntimeTest(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error = %v, output = %s", args, err, string(output))
	}
}

func quoteJSONStringForHookRuntimeTest(value string) string {
	encodedValue, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encodedValue)
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
		cli.WithSession(&sessionUsecaseStub{}),
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
	if got, want := eventStub.logCall.message, "size-threshold"; got != want {
		t.Fatalf("pre-compact log message = %q, want %q (phase marker is retired; source_hook discriminates)", got, want)
	}
	if got, want := eventStub.logCall.sourceHook, "pre_compact"; got != want {
		t.Fatalf("pre-compact log source_hook = %q, want %q", got, want)
	}
}

func TestRootCLI_HookCompactCommand_PreCompactSyncsSessionSummaryWhenEmpty(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("sync-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-pre-compact-sync"),
			types.EventKindCompactSummary,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("sync-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"",
			time.Now(),
		),
	}
	sessionStub := &sessionUsecaseStub{}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"pre_compact_context":"Discussed compact behavior, agreed on PreCompact body sync."}`))
	rootCmd.SetArgs([]string{"hook", "compact", "claude", "pre-compact"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(pre-compact) error = %v", err)
	}
	if got := eventStub.logCall.message; got == "" {
		t.Fatalf("pre-compact log body must not be empty when pre_compact_context is provided")
	}
	got, ok := sessionStub.setSummaryCalls[types.SessionID("sync-session")]
	if !ok {
		t.Fatalf("SetSummaryIfEmpty was not called for the active session")
	}
	if want := "Discussed compact behavior, agreed on PreCompact body sync."; got != want {
		t.Fatalf("SetSummaryIfEmpty body = %q, want %q", got, want)
	}
}

func TestRootCLI_HookCompactCommand_PreCompactSkipsSessionSummarySyncForEmptyBody(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("trigger-only"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-trigger-only"),
			types.EventKindCompactSummary,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("trigger-only"),
			types.Workspace("github.com/duck8823/traceary"),
			"",
			time.Now(),
		),
	}
	sessionStub := &sessionUsecaseStub{}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"trigger":"size-threshold"}`))
	rootCmd.SetArgs([]string{"hook", "compact", "claude", "pre-compact"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(pre-compact) error = %v", err)
	}
	if _, ok := sessionStub.setSummaryCalls[types.SessionID("trigger-only")]; ok {
		t.Fatalf("SetSummaryIfEmpty must not be called when pre_compact_context is empty (trigger-only payload)")
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
		cli.WithSession(&sessionUsecaseStub{}),
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
	if got, want := eventStub.logCall.message, "code-reviewer"; got != want {
		t.Fatalf("subagent-stop log message = %q, want %q (phase marker is retired; source_hook discriminates)", got, want)
	}
	if got, want := eventStub.logCall.sourceHook, "subagent_stop"; got != want {
		t.Fatalf("subagent-stop log source_hook = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSubagentStartCommand_CreatesChildAndActiveState(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "start-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-start-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-start-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"tool_use_id":"toolu_1","tool_input":{"subagent_type":"code-reviewer"}}`))
	rootCmd.SetArgs([]string{"hook", "subagent-start", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-start) error = %v", err)
	}
	if got, want := sessionStub.startChildCall.parent, types.SessionID("parent-session"); got != want {
		t.Fatalf("StartChild parent = %q, want %q", got, want)
	}
	if got, want := sessionStub.startChildCall.childID, types.SessionID("parent-session:sub:toolu_1"); got != want {
		t.Fatalf("StartChild childID = %q, want %q", got, want)
	}
	if got, want := sessionStub.startChildCall.agent, types.Agent("claude/code-reviewer"); got != want {
		t.Fatalf("StartChild agent = %q, want %q", got, want)
	}
	activePath := filepath.Join(stateDir, "active-subagents", "claude-parent-session")
	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatalf("ReadFile(active state) error = %v", err)
	}
	stateJSON := string(data)
	if !strings.Contains(stateJSON, `"toolu_1"`) || !strings.Contains(stateJSON, `"child_session_id":"parent-session:sub:toolu_1"`) {
		t.Fatalf("active child state = %s, want JSON entry for toolu_1", stateJSON)
	}
}

func TestRootCLI_HookSubagentStartCommand_UsesExplicitSessionIDAsParentForOverlappingSiblings(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "explicit-sibling-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{}
	runStart := func(payload string) {
		t.Helper()
		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithSession(sessionStub),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetIn(strings.NewReader(payload))
		rootCmd.SetArgs([]string{"hook", "subagent-start", "claude"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(subagent-start) error = %v", err)
		}
	}

	runStart(`{"session_id":"parent-session","tool_use_id":"toolu_1","tool_input":{"subagent_type":"worker"}}`)
	time.Sleep(time.Millisecond)
	runStart(`{"session_id":"parent-session","tool_use_id":"toolu_2","tool_input":{"subagent_type":"qa"}}`)

	if len(sessionStub.startChildCalls) != 2 {
		t.Fatalf("StartChild calls len = %d, want 2", len(sessionStub.startChildCalls))
	}
	for i, call := range sessionStub.startChildCalls {
		if got, want := call.parent, types.SessionID("parent-session"); got != want {
			t.Fatalf("StartChild call %d parent = %q, want %q", i, got, want)
		}
	}
	if got, want := sessionStub.startChildCalls[0].childID, types.SessionID("parent-session:sub:toolu_1"); got != want {
		t.Fatalf("first childID = %q, want %q", got, want)
	}
	if got, want := sessionStub.startChildCalls[1].childID, types.SessionID("parent-session:sub:toolu_2"); got != want {
		t.Fatalf("second childID = %q, want %q", got, want)
	}

	activeState, err := os.ReadFile(filepath.Join(stateDir, "active-subagents", "claude-parent-session"))
	if err != nil {
		t.Fatalf("ReadFile(parent active state) error = %v", err)
	}
	if !strings.Contains(string(activeState), `"toolu_1"`) || !strings.Contains(string(activeState), `"toolu_2"`) {
		t.Fatalf("parent active state = %s, want both siblings", string(activeState))
	}
	if _, err := os.Stat(filepath.Join(stateDir, "active-subagents", "claude-parent-session:sub:toolu_1")); !os.IsNotExist(err) {
		t.Fatalf("nested active state should not exist; stat err=%v", err)
	}
}

func TestRootCLI_HookSubagentState_TracksOverlappingTaskChildren(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "overlap-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-overlap-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-overlap-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	t.Setenv("TRACEARY_DB_PATH", dbPath)
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	ctx := context.Background()
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := sessionUC.Start(ctx, types.Client("hook"), types.Agent("claude"), types.SessionID("parent-session"), types.Workspace("github.com/duck8823/traceary"), ""); err != nil {
		t.Fatalf("Start(parent) error = %v", err)
	}
	runHook := func(args []string, payload string, eventOverride *eventUsecaseStub) {
		t.Helper()
		opts := []cli.RootCLIOption{
			cli.WithStoreManagement(storeUC),
			cli.WithSession(sessionUC),
			cli.WithDatabasePathSetter(db.SetPath),
		}
		if eventOverride != nil {
			opts = append(opts, cli.WithEvent(eventOverride))
		} else {
			opts = append(opts, cli.WithEvent(eventUC))
		}
		rootCmd := newTestRootCLI(opts...).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetIn(strings.NewReader(payload))
		rootCmd.SetArgs(args)
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
	}

	runHook([]string{"hook", "subagent-start", "claude"}, `{"session_id":"parent-session","tool_use_id":"toolu_1","tool_input":{"subagent_type":"worker"}}`, nil)
	time.Sleep(time.Millisecond)
	runHook([]string{"hook", "subagent-start", "claude"}, `{"session_id":"parent-session","tool_use_id":"toolu_2","tool_input":{"subagent_type":"qa"}}`, nil)
	time.Sleep(time.Millisecond)
	runHook([]string{"hook", "subagent-start", "claude"}, `{"session_id":"parent-session","tool_use_id":"toolu_3","tool_input":{"subagent_type":"planner"}}`, nil)

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	rows, err := sqlDB.Query(`SELECT session_id, spawn_order, ended_at IS NOT NULL FROM sessions WHERE parent_session_id = ? ORDER BY spawn_order`, "parent-session")
	if err != nil {
		t.Fatalf("query children error = %v", err)
	}
	defer func() { _ = rows.Close() }()
	var children []struct {
		id         string
		spawnOrder int
		ended      bool
	}
	for rows.Next() {
		var child struct {
			id         string
			spawnOrder int
			ended      bool
		}
		if err := rows.Scan(&child.id, &child.spawnOrder, &child.ended); err != nil {
			t.Fatalf("Scan(child) error = %v", err)
		}
		children = append(children, child)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Rows error = %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("child rows len = %d, want 3: %#v", len(children), children)
	}
	if children[0].id != "parent-session:sub:toolu_1" || children[0].spawnOrder != 1 {
		t.Fatalf("first child = %#v, want toolu_1 spawn_order 1", children[0])
	}
	if children[1].id != "parent-session:sub:toolu_2" || children[1].spawnOrder != 2 {
		t.Fatalf("second child = %#v, want toolu_2 spawn_order 2", children[1])
	}
	if children[2].id != "parent-session:sub:toolu_3" || children[2].spawnOrder != 3 {
		t.Fatalf("third child = %#v, want toolu_3 spawn_order 3", children[2])
	}

	auditStub := &eventUsecaseStub{}
	runHook([]string{"hook", "audit", "claude"}, `{"session_id":"parent-session","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"ok"}}`, auditStub)
	if got, want := auditStub.auditCall.sessionID, types.SessionID("parent-session:sub:toolu_3"); got != want {
		t.Fatalf("audit sessionID with overlapping children = %q, want %q", got, want)
	}

	runHook([]string{"hook", "subagent-stop", "claude"}, `{"tool_use_id":"toolu_1","subagent_type":"worker"}`, nil)
	var child1Ended, child2Ended, child3Ended bool
	if err := sqlDB.QueryRow(`SELECT ended_at IS NOT NULL FROM sessions WHERE session_id = ?`, "parent-session:sub:toolu_1").Scan(&child1Ended); err != nil {
		t.Fatalf("query child1 ended error = %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT ended_at IS NOT NULL FROM sessions WHERE session_id = ?`, "parent-session:sub:toolu_2").Scan(&child2Ended); err != nil {
		t.Fatalf("query child2 ended error = %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT ended_at IS NOT NULL FROM sessions WHERE session_id = ?`, "parent-session:sub:toolu_3").Scan(&child3Ended); err != nil {
		t.Fatalf("query child3 ended error = %v", err)
	}
	if !child1Ended || child2Ended || child3Ended {
		t.Fatalf("after stopping toolu_1: child1 ended=%v child2 ended=%v child3 ended=%v, want true/false/false", child1Ended, child2Ended, child3Ended)
	}
	activeState, err := os.ReadFile(filepath.Join(stateDir, "active-subagents", "claude-parent-session"))
	if err != nil {
		t.Fatalf("ReadFile(active state after first stop) error = %v", err)
	}
	if strings.Contains(string(activeState), "toolu_1") || !strings.Contains(string(activeState), "toolu_2") || !strings.Contains(string(activeState), "toolu_3") {
		t.Fatalf("active state after stopping toolu_1 = %s, want toolu_2 and toolu_3 active", string(activeState))
	}

	auditStub = &eventUsecaseStub{}
	runHook([]string{"hook", "audit", "claude"}, `{"session_id":"parent-session","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"ok"}}`, auditStub)
	if got, want := auditStub.auditCall.sessionID, types.SessionID("parent-session:sub:toolu_3"); got != want {
		t.Fatalf("audit sessionID after stopping toolu_1 = %q, want %q", got, want)
	}

	runHook([]string{"hook", "subagent-stop", "claude"}, `{"tool_use_id":"toolu_2","subagent_type":"qa"}`, nil)
	auditStub = &eventUsecaseStub{}
	runHook([]string{"hook", "audit", "claude"}, `{"session_id":"parent-session","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"ok"}}`, auditStub)
	if got, want := auditStub.auditCall.sessionID, types.SessionID("parent-session:sub:toolu_3"); got != want {
		t.Fatalf("audit sessionID after stopping toolu_2 = %q, want %q", got, want)
	}

	runHook([]string{"hook", "subagent-stop", "claude"}, `{"tool_use_id":"toolu_3","subagent_type":"planner"}`, nil)
	auditStub = &eventUsecaseStub{}
	runHook([]string{"hook", "audit", "claude"}, `{"session_id":"parent-session","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"ok"}}`, auditStub)
	if got, want := auditStub.auditCall.sessionID, types.SessionID("parent-session"); got != want {
		t.Fatalf("audit sessionID after both children stop = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSubagentState_TracksNestedTaskChildren(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "nested-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-nested-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-nested-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	t.Setenv("TRACEARY_DB_PATH", dbPath)
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	ctx := context.Background()
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := sessionUC.Start(ctx, types.Client("hook"), types.Agent("claude"), types.SessionID("parent-session"), types.Workspace("github.com/duck8823/traceary"), ""); err != nil {
		t.Fatalf("Start(parent) error = %v", err)
	}
	runHook := func(args []string, payload string, eventOverride *eventUsecaseStub) {
		t.Helper()
		opts := []cli.RootCLIOption{
			cli.WithStoreManagement(storeUC),
			cli.WithSession(sessionUC),
			cli.WithDatabasePathSetter(db.SetPath),
		}
		if eventOverride != nil {
			opts = append(opts, cli.WithEvent(eventOverride))
		} else {
			opts = append(opts, cli.WithEvent(eventUC))
		}
		rootCmd := newTestRootCLI(opts...).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetIn(strings.NewReader(payload))
		rootCmd.SetArgs(args)
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
	}

	runHook([]string{"hook", "subagent-start", "claude"}, `{"session_id":"parent-session","tool_use_id":"toolu_child","tool_input":{"subagent_type":"worker"}}`, nil)
	runHook([]string{"hook", "subagent-start", "claude"}, `{"session_id":"parent-session:sub:toolu_child","tool_use_id":"toolu_grandchild","tool_input":{"subagent_type":"qa"}}`, nil)

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	var childParent, grandchildParent string
	var childOrder, grandchildOrder int
	if err := sqlDB.QueryRow(`SELECT parent_session_id, spawn_order FROM sessions WHERE session_id = ?`, "parent-session:sub:toolu_child").Scan(&childParent, &childOrder); err != nil {
		t.Fatalf("query child error = %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT parent_session_id, spawn_order FROM sessions WHERE session_id = ?`, "parent-session:sub:toolu_child:sub:toolu_grandchild").Scan(&grandchildParent, &grandchildOrder); err != nil {
		t.Fatalf("query grandchild error = %v", err)
	}
	if childParent != "parent-session" || childOrder != 1 {
		t.Fatalf("child parent/order = %q/%d, want parent-session/1", childParent, childOrder)
	}
	if grandchildParent != "parent-session:sub:toolu_child" || grandchildOrder != 1 {
		t.Fatalf("grandchild parent/order = %q/%d, want child/1", grandchildParent, grandchildOrder)
	}

	auditStub := &eventUsecaseStub{}
	runHook([]string{"hook", "audit", "claude"}, `{"session_id":"parent-session","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"ok"}}`, auditStub)
	if got, want := auditStub.auditCall.sessionID, types.SessionID("parent-session:sub:toolu_child:sub:toolu_grandchild"); got != want {
		t.Fatalf("nested audit sessionID = %q, want %q", got, want)
	}

	runHook([]string{"hook", "subagent-stop", "claude"}, `{"session_id":"parent-session","tool_use_id":"toolu_grandchild","subagent_type":"qa"}`, nil)
	auditStub = &eventUsecaseStub{}
	runHook([]string{"hook", "audit", "claude"}, `{"session_id":"parent-session","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"ok"}}`, auditStub)
	if got, want := auditStub.auditCall.sessionID, types.SessionID("parent-session:sub:toolu_child"); got != want {
		t.Fatalf("nested audit after grandchild stop sessionID = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSubagentState_PrunesOrphanedActiveChild(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "orphan-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	activeDir := filepath.Join(stateDir, "active-subagents")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-orphan-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	staleStartedAt := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	activeJSON := `{"children":{"toolu_stale":{"child_session_id":"parent-session:sub:toolu_stale","started_at":"` + staleStartedAt + `"}}}`
	if err := os.WriteFile(filepath.Join(activeDir, "claude-parent-session"), []byte(activeJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(active state) error = %v", err)
	}

	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"parent-session","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"ok"}}`))
	rootCmd.SetArgs([]string{"hook", "audit", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(audit) error = %v", err)
	}
	if got, want := eventStub.auditCall.sessionID, types.SessionID("parent-session"); got != want {
		t.Fatalf("audit sessionID after orphan pruning = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(activeDir, "claude-parent-session")); !os.IsNotExist(err) {
		t.Fatalf("stale active state should be removed; stat err=%v", err)
	}
}

func TestRootCLI_HookSubagentStartCommand_SynthesizesToolUseIDFromEventID(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "missing-tool-use-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-missing-tool-use-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"event_id":"evt_123","tool_input":{"subagent_type":"worker"}}`))
	rootCmd.SetArgs([]string{"hook", "subagent-start", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-start) error = %v", err)
	}
	if got, want := sessionStub.startChildCall.childID, types.SessionID("parent-session:sub:event-evt_123"); got != want {
		t.Fatalf("StartChild childID = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSubagentStopCommand_UsesLatestActiveChildWhenToolUseIDMissing(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "stop-missing-tool-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	activeDir := filepath.Join(stateDir, "active-subagents")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-stop-missing-tool-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	olderStartedAt := time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339)
	newerStartedAt := time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
	activeJSON := `{"children":{` +
		`"toolu_1":{"child_session_id":"parent-session:sub:toolu_1","started_at":"` + olderStartedAt + `"},` +
		`"toolu_2":{"child_session_id":"parent-session:sub:toolu_2","started_at":"` + newerStartedAt + `"}` +
		`}}`
	if err := os.WriteFile(filepath.Join(activeDir, "claude-parent-session"), []byte(activeJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(active state) error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{}
	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"subagent_type":"worker"}`))
	rootCmd.SetArgs([]string{"hook", "subagent-stop", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-stop) error = %v", err)
	}
	if got, want := sessionStub.endCall.sessionID, types.SessionID("parent-session:sub:toolu_2"); got != want {
		t.Fatalf("End child sessionID = %q, want %q", got, want)
	}
	activeState, err := os.ReadFile(filepath.Join(activeDir, "claude-parent-session"))
	if err != nil {
		t.Fatalf("ReadFile(active state after missing-id stop) error = %v", err)
	}
	if strings.Contains(string(activeState), "toolu_2") || !strings.Contains(string(activeState), "toolu_1") {
		t.Fatalf("active state after missing-id stop = %s, want only older toolu_1 remaining", string(activeState))
	}
}

func TestRootCLI_HookAuditCommand_UsesActiveSubagentSession(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "audit-child-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	activeDir := filepath.Join(stateDir, "active-subagents")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-audit-child-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	startedAt := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(activeDir, "claude-parent-session"), []byte(`{"children":{"toolu_1":{"child_session_id":"parent-session:sub:toolu_1","started_at":"`+startedAt+`"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(active state) error = %v", err)
	}

	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"parent-session","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"ok"}}`))
	rootCmd.SetArgs([]string{"hook", "audit", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(audit) error = %v", err)
	}
	if got, want := eventStub.auditCall.sessionID, types.SessionID("parent-session:sub:toolu_1"); got != want {
		t.Fatalf("audit sessionID = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSubagentStopCommand_EndsChildAndClearsActiveState(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "stop-child-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	activeDir := filepath.Join(stateDir, "active-subagents")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-stop-child-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	startedAt := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(activeDir, "claude-parent-session"), []byte(`{"children":{"toolu_1":{"child_session_id":"parent-session:sub:toolu_1","started_at":"`+startedAt+`"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(active state) error = %v", err)
	}
	sessionStub := &sessionUsecaseStub{}
	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"tool_use_id":"toolu_1","subagent_type":"code-reviewer"}`))
	rootCmd.SetArgs([]string{"hook", "subagent-stop", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-stop) error = %v", err)
	}
	if got, want := sessionStub.endCall.sessionID, types.SessionID("parent-session:sub:toolu_1"); got != want {
		t.Fatalf("End child sessionID = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.sessionID, types.SessionID("parent-session"); got != want {
		t.Fatalf("back-compat log sessionID = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(activeDir, "claude-parent-session")); !os.IsNotExist(err) {
		t.Fatalf("active state should be cleared after final child stops; stat err=%v", err)
	}
}

func TestRootCLI_HookSubagentStopCommand_LazilySynthesizesMissingStart(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "lazy-child-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-lazy-child-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{}
	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"tool_use_id":"toolu_legacy","subagent_type":"reviewer"}`))
	rootCmd.SetArgs([]string{"hook", "subagent-stop", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-stop) error = %v", err)
	}
	if got, want := sessionStub.startChildCall.childID, types.SessionID("parent-session:sub:toolu_legacy"); got != want {
		t.Fatalf("lazy StartChild childID = %q, want %q", got, want)
	}
	if got, want := sessionStub.endCall.sessionID, types.SessionID("parent-session:sub:toolu_legacy"); got != want {
		t.Fatalf("lazy End child sessionID = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSubagentStopCommand_LazySynthesisKeepsParentAgent(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "lazy-plan-key")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-lazy-plan-key"), []byte("parent-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-lazy-plan-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	t.Setenv("TRACEARY_DB_PATH", dbPath)
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	ctx := context.Background()
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := sessionUC.Start(ctx, types.Client("hook"), types.Agent("claude"), types.SessionID("parent-session"), types.Workspace("github.com/duck8823/traceary"), ""); err != nil {
		t.Fatalf("Start(parent) error = %v", err)
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithSession(sessionUC),
		cli.WithEvent(eventUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"parent-session","tool_use_id":"toolu_plan","agent_type":"Plan","subagent_type":"Plan"}`))
	rootCmd.SetArgs([]string{"hook", "subagent-stop", "claude"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-stop) error = %v", err)
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	var parentAgent string
	if err := sqlDB.QueryRow(`SELECT agent FROM sessions WHERE session_id = ?`, "parent-session").Scan(&parentAgent); err != nil {
		t.Fatalf("query parent agent error = %v", err)
	}
	if parentAgent != "claude" {
		t.Fatalf("parent session agent = %q, want claude", parentAgent)
	}
	var childParent, childAgent, childKind string
	if err := sqlDB.QueryRow(`SELECT COALESCE(parent_session_id, ''), agent, subagent_kind FROM sessions WHERE session_id = ?`, "parent-session:sub:toolu_plan").Scan(&childParent, &childAgent, &childKind); err != nil {
		t.Fatalf("query child session error = %v", err)
	}
	if childParent != "parent-session" || childAgent != "claude/Plan" || childKind != "task" {
		t.Fatalf("child metadata = parent:%q agent:%q kind:%q, want parent-session claude/Plan task", childParent, childAgent, childKind)
	}
	var parentPlanEvents int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM events WHERE session_id = ? AND agent = ?`, "parent-session", "claude/Plan").Scan(&parentPlanEvents); err != nil {
		t.Fatalf("query parent Plan event count error = %v", err)
	}
	if parentPlanEvents != 0 {
		t.Fatalf("parent claude/Plan events = %d, want 0", parentPlanEvents)
	}

	stdout := &bytes.Buffer{}
	topCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithSession(sessionUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	topCmd.SetOut(stdout)
	topCmd.SetErr(&bytes.Buffer{})
	topCmd.SetArgs([]string{"top", "--snapshot", "--json", "--db-path", dbPath})
	if err := topCmd.Execute(); err != nil {
		t.Fatalf("Execute(top --snapshot --json) error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"agents": [
        "claude"
      ]`) {
		t.Fatalf("top JSON parent agents should stay claude only, got: %s", output)
	}
	if strings.Contains(output, `"session_id": "parent-session:sub:toolu_plan"`) {
		t.Fatalf("top JSON should prune ended synthesized Plan child, got: %s", output)
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
	// #662 changed the transcript body shape from a flat string to a
	// JSON block envelope so downstream readers can distinguish
	// thinking from rendered reply. Assert the parsed block structure
	// instead of a concat string.
	gotBlocks := apptypes.ParseEventBodyBlocks(eventStub.logCall.message)
	wantBlocks := []apptypes.EventBodyBlock{
		{Type: apptypes.EventBodyBlockTypeThinking, Text: "first-block thinking"},
		{Type: apptypes.EventBodyBlockTypeText, Text: "newer reasoning"},
		{Type: apptypes.EventBodyBlockTypeText, Text: "with two blocks"},
	}
	if diff := cmp.Diff(wantBlocks, gotBlocks); diff != "" {
		t.Fatalf("transcript blocks mismatch (-want +got):\n%s", diff)
	}
}

// TestRootCLI_HookTranscriptCommand_PassesExtraRedactPatternsThroughLogCfg
// asserts that operator-configured extra_redact_patterns are handed to
// EventUsecase.Log via LogRedaction (the usecase performs the actual
// redaction; CLI / hooks only select the policy). Before #626 the
// transcript hook did the redaction in the presentation layer itself;
// #666 consolidated it inside EventUsecase.Log so this test now only
// verifies the config hand-off.
func TestRootCLI_HookTranscriptCommand_PassesExtraRedactPatternsThroughLogCfg(t *testing.T) {
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

	got := eventStub.logCall.logCfg.ExtraRedactPatterns()
	want := []string{`my_custom_secret=\S+`}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("logCfg.ExtraRedactPatterns() mismatch (-want +got):\n%s", diff)
	}
	if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
		t.Errorf("log kind = %q, want %q", got, want)
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
	// #662: the surviving assistant entry is serialized as a single
	// text block — the malformed line is skipped without aborting.
	gotBlocks := apptypes.ParseEventBodyBlocks(eventStub.logCall.message)
	wantBlocks := []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: "valid assistant reply"}}
	if diff := cmp.Diff(wantBlocks, gotBlocks); diff != "" {
		t.Fatalf("transcript blocks mismatch (-want +got):\n%s", diff)
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
	// Exercise both the extra pattern (operator secret shape) and a
	// builtin redactor (Authorization: Bearer header) on the same
	// payload so the Codex path cannot regress either branch silently.
	payload := `{"last_assistant_message":"Codex reply body. Authorization: Bearer abc.DEF-123 and my_custom_secret=s3cr3tValue42 follows.","session_id":"codex-transcript-session"}`
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
	// Regression guard: the transcript hook must keep using the
	// workspace persisted at session start, not a drifted cwd /
	// TRACEARY_WORKSPACE override. Without this assertion the
	// session_started / session_ended rows could stay on the original
	// workspace while transcript silently moves to a different one.
	if got, want := eventStub.logCall.workspace, types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Errorf("transcript log workspace = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.sessionID, types.SessionID("codex-transcript-session"); got != want {
		t.Errorf("transcript log sessionID = %q, want %q", got, want)
	}
	// #662: Codex emits a single text block (no thinking/text
	// distinction on the host side), verbatim from
	// last_assistant_message.
	gotBlocks := apptypes.ParseEventBodyBlocks(eventStub.logCall.message)
	if len(gotBlocks) != 1 || gotBlocks[0].Type != apptypes.EventBodyBlockTypeText {
		t.Fatalf("transcript blocks = %+v, want single text block", gotBlocks)
	}
	if !strings.HasPrefix(gotBlocks[0].Text, "Codex reply body.") {
		t.Errorf("transcript block text prefix = %q, want it to start with \"Codex reply body.\"", gotBlocks[0].Text)
	}
	// Redaction now runs inside EventUsecase.Log (#666). The CLI
	// layer's job is to hand the operator-configured extra patterns
	// over via LogRedaction; the actual redaction behaviour is
	// covered by application/usecase/record_log_test.go.
	if diff := cmp.Diff([]string{`my_custom_secret=\S+`}, eventStub.logCall.logCfg.ExtraRedactPatterns()); diff != "" {
		t.Errorf("logCfg.ExtraRedactPatterns() mismatch (-want +got):\n%s", diff)
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
	// Exercise both the extra pattern (operator secret shape) and a
	// builtin redactor (Authorization: Bearer header) on the same
	// payload so the Gemini path cannot regress either branch silently.
	payload := `{"prompt_response":"Gemini summary. Authorization: Bearer abc.DEF-123 and my_custom_secret=s3cr3tValue42 trailing.","session_id":"gemini-transcript-session"}`
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
	// Regression guard: the transcript hook must keep using the
	// workspace persisted at session start, not a drifted cwd /
	// TRACEARY_WORKSPACE override. See the Codex test comment — same
	// concern applies to Gemini.
	if got, want := eventStub.logCall.workspace, types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Errorf("transcript log workspace = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.sessionID, types.SessionID("gemini-transcript-session"); got != want {
		t.Errorf("transcript log sessionID = %q, want %q", got, want)
	}
	// #662: Gemini emits a single text block from prompt_response.
	gotBlocks := apptypes.ParseEventBodyBlocks(eventStub.logCall.message)
	if len(gotBlocks) != 1 || gotBlocks[0].Type != apptypes.EventBodyBlockTypeText {
		t.Fatalf("transcript blocks = %+v, want single text block", gotBlocks)
	}
	if !strings.HasPrefix(gotBlocks[0].Text, "Gemini summary.") {
		t.Errorf("transcript block text prefix = %q, want it to start with \"Gemini summary.\"", gotBlocks[0].Text)
	}
	// Same split as the Codex test: CLI hands LogRedaction over,
	// EventUsecase.Log does the actual redaction (covered in
	// application/usecase/record_log_test.go).
	if diff := cmp.Diff([]string{`my_custom_secret=\S+`}, eventStub.logCall.logCfg.ExtraRedactPatterns()); diff != "" {
		t.Errorf("logCfg.ExtraRedactPatterns() mismatch (-want +got):\n%s", diff)
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

func TestRootCLI_HookPromptCommand_RecordsGeminiBeforeAgentPayload(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "gemini-test-key"), []byte("gemini-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "gemini-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-prompt"),
			types.EventKindPrompt,
			types.Client("hook"),
			types.Agent("gemini"),
			types.SessionID("gemini-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"Refactor module",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	// Payload mirrors the Gemini CLI 0.36.x BeforeAgent schema: the agent
	// hook fires after the user submits a prompt and the only field
	// Traceary needs is `prompt`. Other Gemini-specific fields are
	// ignored.
	rootCmd.SetIn(strings.NewReader(`{
		"hook_event_name": "BeforeAgent",
		"prompt": "Refactor module"
	}`))
	rootCmd.SetArgs([]string{"hook", "prompt", "gemini"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := eventStub.logCall.kind, types.EventKindPrompt; got != want {
		t.Fatalf("prompt log kind = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.message, "Refactor module"; got != want {
		t.Fatalf("prompt log message = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.agent, types.Agent("gemini"); got != want {
		t.Fatalf("prompt log agent = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.sessionID, types.SessionID("gemini-session"); got != want {
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
