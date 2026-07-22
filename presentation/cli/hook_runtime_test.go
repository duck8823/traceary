package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

type hookMemoryExtractJobFixture struct {
	SessionID      string `json:"session_id"`
	Workspace      string `json:"workspace"`
	SourceBoundary string `json:"source_boundary"`
}

func readHookMemoryExtractJobForTest(t *testing.T, path string) hookMemoryExtractJobFixture {
	t.Helper()
	if path == "" {
		t.Fatal("memory extraction worker was not launched")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(memory extraction job) error = %v", err)
	}
	var job hookMemoryExtractJobFixture
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatalf("json.Unmarshal(memory extraction job) error = %v", err)
	}
	return job
}

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

func TestRootCLI_HookSessionCommand_OneShotWrapperOverridesHostIdentity(t *testing.T) {
	t.Setenv("TRACEARY_RUNTIME_MODE", "one_shot")
	t.Setenv("TRACEARY_RUNTIME_SESSION_ID", "wrapper-session")
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
		types.EventID("event-wrapper"), types.EventKindSessionStarted, "hook", "codex", "wrapper-session", "duck8823/traceary", "session started", time.Now(),
	)}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetIn(strings.NewReader(`{"session_id":"host-session","cwd":"duck8823/traceary"}`))
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"hook", "session", "codex", "start"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := sessionStub.startCall.sessionID; got != "wrapper-session" {
		t.Fatalf("StartWithRuntimeMode session ID = %q, want wrapper-session", got)
	}
	if got := sessionStub.startCall.runtimeMode; got != types.RuntimeModeOneShot {
		t.Fatalf("StartWithRuntimeMode runtime mode = %q, want one_shot", got)
	}
	if got := sessionStub.startCall.parentSessionID; got != "" {
		t.Fatalf("StartWithRuntimeMode parent session ID = %q, want empty", got)
	}
}

func TestRootCLI_HookSessionCommand_StartRunsRateLimitedSessionGC(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "gc-key")
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_DB_PATH", filepath.Join(t.TempDir(), "traceary.db"))
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	storeStub := &storeManagementUsecaseStub{}
	sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
		types.EventID("evt-gc-start"),
		types.EventKindSessionStarted,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID("gc-session"),
		types.Workspace("github.com/duck8823/traceary"),
		"session started",
		time.Now(),
	)}
	runStart := func() {
		t.Helper()
		cmd := newTestRootCLI(cli.WithStoreManagement(storeStub), cli.WithSession(sessionStub)).Command()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader(`{"session_id":"gc-session"}`))
		cmd.SetArgs([]string{"hook", "session", "codex", "start"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute(session start) error = %v", err)
		}
	}

	runStart()
	runStart()
	if got, want := len(storeStub.staleCalls), 1; got != want {
		t.Fatalf("CloseStaleSessions calls within rate window = %d, want %d", got, want)
	}
	if call := storeStub.staleCalls[0]; call.staleAfter != 24*time.Hour || call.dryRun {
		t.Fatalf("CloseStaleSessions call = %+v, want 24h non-dry-run", call)
	} else if !slices.Contains(call.protectedSessionIDs, types.SessionID("gc-session")) {
		t.Fatalf("protected session IDs = %q, want gc-session", call.protectedSessionIDs)
	}
	markers, err := filepath.Glob(filepath.Join(os.Getenv("TRACEARY_HOOK_STATE_DIR"), "session-gc", "*.stamp"))
	if err != nil || len(markers) != 1 {
		t.Fatalf("session GC markers = %v, err = %v, want one", markers, err)
	}
	old := time.Now().Add(-7 * time.Hour)
	if err := os.Chtimes(markers[0], old, old); err != nil {
		t.Fatalf("Chtimes(marker) error = %v", err)
	}
	runStart()
	if got, want := len(storeStub.staleCalls), 2; got != want {
		t.Fatalf("CloseStaleSessions calls after rate window = %d, want %d", got, want)
	}
}

func TestRootCLI_HookSessionCommand_ConcurrentStartsRunOneSessionGC(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "gc-concurrent-key")
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_DB_PATH", filepath.Join(t.TempDir(), "traceary.db"))
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	storeStub := &storeManagementUsecaseStub{staleDelay: 50 * time.Millisecond}
	const invocations = 8
	start := make(chan struct{})
	errs := make(chan error, invocations)
	var wg sync.WaitGroup
	for i := range invocations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			sessionID := fmt.Sprintf("gc-concurrent-%d", i)
			sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
				types.EventID("evt-"+sessionID),
				types.EventKindSessionStarted,
				types.Client("hook"),
				types.Agent("codex"),
				types.SessionID(sessionID),
				types.Workspace("github.com/duck8823/traceary"),
				"session started",
				time.Now(),
			)}
			cmd := newTestRootCLI(cli.WithStoreManagement(storeStub), cli.WithSession(sessionStub)).Command()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(fmt.Sprintf(`{"session_id":%q}`, sessionID)))
			cmd.SetArgs([]string{"hook", "session", "codex", "start"})
			errs <- cmd.Execute()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent session start error = %v", err)
		}
	}
	storeStub.staleMu.Lock()
	defer storeStub.staleMu.Unlock()
	if got := len(storeStub.staleCalls); got != 1 {
		t.Fatalf("CloseStaleSessions concurrent calls = %d, want 1", got)
	}
}

func TestRootCLI_HookSessionCommand_StartConvergesStaleStoreWithoutClosingRecentActivity(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "gc-integration-key")
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	t.Setenv("TRACEARY_DB_PATH", dbPath)

	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeDS := sqliteinfra.NewStoreManagementDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(storeDS)
	if err := storeUC.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	now := time.Now().UTC()
	for _, sessionID := range []string{"idle-old", "active-old"} {
		if _, err := sqlDB.Exec(`INSERT INTO sessions(session_id, started_at) VALUES (?, ?)`, sessionID, now.Add(-48*time.Hour).Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert %s: %v", sessionID, err)
		}
	}
	if _, err := sqlDB.Exec(`INSERT INTO events(id, kind, client, agent, session_id, workspace, body, source_hook, created_at) VALUES ('recent-event', 'note', 'hook', 'codex', 'active-old', '', '', '', ?)`, now.Add(-time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert recent event: %v", err)
	}

	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	cmd := newTestRootCLI(cli.WithStoreManagement(storeUC), cli.WithSession(sessionUC), cli.WithDatabasePathSetter(db.SetPath)).Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(`{"session_id":"new-session"}`))
	cmd.SetArgs([]string{"hook", "session", "codex", "start"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(session start) error = %v", err)
	}
	for _, tc := range []struct {
		sessionID  string
		wantClosed bool
	}{
		{sessionID: "idle-old", wantClosed: true},
		{sessionID: "active-old", wantClosed: false},
		{sessionID: "new-session", wantClosed: false},
	} {
		var endedAt sql.NullString
		if err := sqlDB.QueryRow(`SELECT ended_at FROM sessions WHERE session_id = ?`, tc.sessionID).Scan(&endedAt); err != nil {
			t.Fatalf("select %s ended_at: %v", tc.sessionID, err)
		}
		if endedAt.Valid != tc.wantClosed {
			t.Fatalf("%s closed = %v, want %v", tc.sessionID, endedAt.Valid, tc.wantClosed)
		}
	}
}

func TestRootCLI_HookSessionCommand_StartRetriesSessionGCAfterFailure(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "gc-retry-key")
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_DB_PATH", filepath.Join(t.TempDir(), "traceary.db"))

	storeStub := &storeManagementUsecaseStub{staleErr: errors.New("busy")}
	sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(types.EventID("evt-gc-retry"), types.EventKindSessionStarted, types.Client("hook"), types.Agent("codex"), types.SessionID("gc-retry-session"), "", "session started", time.Now())}
	for range 2 {
		cmd := newTestRootCLI(cli.WithStoreManagement(storeStub), cli.WithSession(sessionStub)).Command()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader(`{"session_id":"gc-retry-session"}`))
		cmd.SetArgs([]string{"hook", "session", "codex", "start"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("session start must ignore opportunistic GC failure: %v", err)
		}
	}
	if got, want := len(storeStub.staleCalls), 2; got != want {
		t.Fatalf("CloseStaleSessions retry calls = %d, want %d", got, want)
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

func TestRootCLI_HookSessionCommand_EndUsesStateAndCreatesEndMarker(t *testing.T) {
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
	rootCmd.SetArgs([]string{"hook", "session", "claude", "end"})

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
	diagnosticsDir := filepath.Join(stateDir, "diagnostics")
	if entries, err := os.ReadDir(diagnosticsDir); err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir(diagnostics) error = %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("diagnostics entries = %d, want none after successful SessionEnd", len(entries))
	}
}

func TestRootCLI_HookSessionCommand_EndLeavesCancellationDiagnosticOnFailure(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "cancel-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-cancel-key"), []byte("cancelled-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-cancel-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{endErr: errors.New("simulated hook cancellation")}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "end"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v (hook errors must stay best-effort)", err)
	}

	diagnosticsDir := filepath.Join(stateDir, "diagnostics")
	entries, err := os.ReadDir(diagnosticsDir)
	if err != nil {
		t.Fatalf("ReadDir(diagnostics) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("diagnostics entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(diagnosticsDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(diagnostic) error = %v", err)
	}
	var diagnostic struct {
		Client      string `json:"client"`
		HostEvent   string `json:"host_event"`
		HookCommand string `json:"hook_command"`
		HookPath    string `json:"hook_path"`
		Workspace   string `json:"workspace"`
		SessionID   string `json:"session_id"`
		Status      string `json:"status"`
		StartedAt   string `json:"started_at"`
	}
	if err := json.Unmarshal(data, &diagnostic); err != nil {
		t.Fatalf("json.Unmarshal(diagnostic) error = %v", err)
	}
	if got, want := diagnostic.Client, "claude"; got != want {
		t.Fatalf("diagnostic client = %q, want %q", got, want)
	}
	if got, want := diagnostic.HostEvent, "SessionEnd"; got != want {
		t.Fatalf("diagnostic host_event = %q, want %q", got, want)
	}
	if got, want := diagnostic.HookCommand, "'traceary' 'hook' 'session' 'claude' 'end'"; got != want {
		t.Fatalf("diagnostic hook_command = %q, want %q", got, want)
	}
	if diagnostic.HookPath == "" {
		t.Fatal("diagnostic hook_path is empty")
	}
	if got, want := diagnostic.Workspace, "github.com/duck8823/traceary"; got != want {
		t.Fatalf("diagnostic workspace = %q, want %q", got, want)
	}
	if got, want := diagnostic.SessionID, "cancelled-session"; got != want {
		t.Fatalf("diagnostic session_id = %q, want %q", got, want)
	}
	if got, want := diagnostic.Status, "started"; got != want {
		t.Fatalf("diagnostic status = %q, want %q", got, want)
	}
	if diagnostic.StartedAt == "" {
		t.Fatal("diagnostic started_at is empty")
	}
	malformedHash := hookCancellationDiagnosticSessionHashForTest("claude", "SessionEnd", "cancelled-session")
	malformedName := fmt.Sprintf("claude-SessionEnd-cancelled-session-%s-malformed-20260619T000000.000000000Z.json", malformedHash)
	malformedPath := filepath.Join(diagnosticsDir, malformedName)
	if err := os.WriteFile(malformedPath, []byte(`{"client":"claude"`), 0o600); err != nil {
		t.Fatalf("WriteFile(malformed diagnostic) error = %v", err)
	}

	successStub := &sessionUsecaseStub{
		endEvent: model.EventOf(
			types.EventID("evt-end-after-cancel"),
			types.EventKindSessionEnded,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("cancelled-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"session ended",
			time.Now(),
		),
	}
	successCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(successStub),
	).Command()
	successCmd.SetOut(&bytes.Buffer{})
	successCmd.SetErr(&bytes.Buffer{})
	successCmd.SetIn(strings.NewReader(`{"agent_type":"planner"}`))
	successCmd.SetArgs([]string{"hook", "session", "claude", "end"})

	if err := successCmd.Execute(); err != nil {
		t.Fatalf("success Execute() error = %v", err)
	}
	entries, err = os.ReadDir(diagnosticsDir)
	if err != nil {
		t.Fatalf("ReadDir(diagnostics after retry) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("diagnostics entries after successful retry = %d, want none", len(entries))
	}
}

// hookCancellationDiagnosticSessionHashForTest mirrors the production hash
// embedded in diagnostic filenames so malformed-marker fixtures use the same
// scheme that unreadable cleanup matches against.
func hookCancellationDiagnosticSessionHashForTest(client, hostEvent, sessionID string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(client),
		strings.TrimSpace(hostEvent),
		strings.TrimSpace(sessionID),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "s" + hex.EncodeToString(sum[:])[:12]
}

func TestRootCLI_HookSessionCommand_EndWritesCancellationDiagnosticBeforeWorkspaceResolution(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "workspace-error-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-workspace-error-key"), []byte("workspace-error-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(stateDir, "claude-workspace-error-key-repo"), 0o755); err != nil {
		t.Fatalf("Mkdir(workspace state path) error = %v", err)
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "end"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v (hook errors must stay best-effort)", err)
	}

	diagnosticsDir := filepath.Join(stateDir, "diagnostics")
	entries, err := os.ReadDir(diagnosticsDir)
	if err != nil {
		t.Fatalf("ReadDir(diagnostics) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("diagnostics entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(diagnosticsDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(diagnostic) error = %v", err)
	}
	var diagnostic struct {
		HostEvent string `json:"host_event"`
		Workspace string `json:"workspace"`
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(data, &diagnostic); err != nil {
		t.Fatalf("json.Unmarshal(diagnostic) error = %v", err)
	}
	if got, want := diagnostic.HostEvent, "SessionEnd"; got != want {
		t.Fatalf("diagnostic host_event = %q, want %q", got, want)
	}
	if got, want := diagnostic.SessionID, "workspace-error-session"; got != want {
		t.Fatalf("diagnostic session_id = %q, want %q", got, want)
	}
	if got, want := diagnostic.Status, "started"; got != want {
		t.Fatalf("diagnostic status = %q, want %q", got, want)
	}
	if diagnostic.Workspace != "" {
		t.Fatalf("diagnostic workspace = %q, want empty when workspace resolution fails after marker creation", diagnostic.Workspace)
	}
}

func TestRootCLI_HookSessionCommand_StopQueuesMemoryAutoExtract(t *testing.T) {
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
	var launchedJobPath string
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
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			launchedJobPath = path
			return nil
		}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"agent_type":"planner"}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "stop"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	job := readHookMemoryExtractJobForTest(t, launchedJobPath)
	if got, want := job.SessionID, "auto-extract-session"; got != want {
		t.Fatalf("queued session ID = %q, want %q", got, want)
	}
	if got, want := job.Workspace, "github.com/duck8823/traceary"; got != want {
		t.Fatalf("queued workspace = %q, want %q", got, want)
	}
	if got, want := job.SourceBoundary, "turn_boundary"; got != want {
		t.Fatalf("queued source boundary = %q, want %q", got, want)
	}
	if got := memoryStub.extractCriteria.SessionID(); got != "" {
		t.Fatalf("synchronous extraction session ID = %q, want empty", got)
	}
	// stop is a turn boundary (#1170): the session must stay open and
	// the hook state must survive so later prompts/audits resolve to
	// the same session.
	if got := sessionStub.endCall.sessionID; got != "" {
		t.Fatalf("session.End called with %q, want no End on stop", got)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "claude-test-key-extract")); err != nil {
		t.Fatalf("session state should survive stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "claude-test-key-extract-repo")); err != nil {
		t.Fatalf("workspace state should survive stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "ended", "claude-auto-extract-session")); !os.IsNotExist(err) {
		t.Fatalf("stop must not create an end marker: %v", err)
	}

	workerCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	workerCmd.SetOut(&bytes.Buffer{})
	workerCmd.SetErr(&bytes.Buffer{})
	workerCmd.SetArgs([]string{"hook", "memory-extract-worker", "--job", launchedJobPath})
	if err := workerCmd.Execute(); err != nil {
		t.Fatalf("worker Execute() error = %v", err)
	}
	if got, want := memoryStub.extractCriteria.SessionID(), types.SessionID("auto-extract-session"); got != want {
		t.Fatalf("worker extraction session ID = %q, want %q", got, want)
	}
	if got, want := memoryStub.extractCriteria.Workspace(), types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Fatalf("worker extraction workspace = %q, want %q", got, want)
	}
	if _, err := os.Stat(launchedJobPath); !os.IsNotExist(err) {
		t.Fatalf("successful worker must remove job: %v", err)
	}
}

func TestRootCLI_HookSessionCommand_EndQueuesMemoryAutoExtractWithoutBlocking(t *testing.T) {
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
	var launchedJobPath string
	primaryCleanupCompleteAtLaunch := false
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
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			launchedJobPath = path
			_, sessionErr := os.Stat(filepath.Join(stateDir, "claude-test-key-extract-fail"))
			_, workspaceErr := os.Stat(filepath.Join(stateDir, "claude-test-key-extract-fail-repo"))
			_, markerErr := os.Stat(filepath.Join(stateDir, "ended", "claude-auto-extract-fail-session"))
			primaryCleanupCompleteAtLaunch = os.IsNotExist(sessionErr) && os.IsNotExist(workspaceErr) && markerErr == nil
			return nil
		}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{}`))
	rootCmd.SetArgs([]string{"hook", "session", "claude", "end"})

	// runHookBestEffort swallows the error in production, but the test
	// runtime returns nil too — auto-extract failure must NEVER block
	// the session-end record from committing.
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v (auto-extract failure must not propagate)", err)
	}
	if got, want := sessionStub.endCall.sessionID, types.SessionID("auto-extract-fail-session"); got != want {
		t.Fatalf("session.End sessionID = %q, want %q (must be invoked even when extract fails)", got, want)
	}
	if !primaryCleanupCompleteAtLaunch {
		t.Fatal("worker launched before session-end hook state cleanup completed")
	}
	job := readHookMemoryExtractJobForTest(t, launchedJobPath)
	if got, want := job.SourceBoundary, "session_end"; got != want {
		t.Fatalf("queued source boundary = %q, want %q", got, want)
	}
	if got := memoryStub.extractCriteria.SessionID(); got != "" {
		t.Fatalf("synchronous extraction session ID = %q, want empty", got)
	}

	workerCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	workerCmd.SetOut(&bytes.Buffer{})
	workerCmd.SetErr(&bytes.Buffer{})
	workerCmd.SetArgs([]string{"hook", "memory-extract-worker", "--job", launchedJobPath})
	if err := workerCmd.Execute(); err == nil {
		t.Fatal("worker Execute() error = nil, want extraction failure")
	}
	data, err := os.ReadFile(launchedJobPath)
	if err != nil {
		t.Fatalf("ReadFile(failed job) error = %v", err)
	}
	var failedJob struct {
		Attempts      int    `json:"attempts"`
		LastAttemptAt string `json:"last_attempt_at"`
		LastError     string `json:"last_error"`
	}
	if err := json.Unmarshal(data, &failedJob); err != nil {
		t.Fatalf("json.Unmarshal(failed job) error = %v", err)
	}
	if failedJob.Attempts != 1 || failedJob.LastAttemptAt == "" || !strings.Contains(failedJob.LastError, "simulated extract failure") {
		t.Fatalf("failed job metadata = %+v, want retryable attempt", failedJob)
	}
	memoryStub.extractErr = nil
	retryCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	retryCmd.SetOut(&bytes.Buffer{})
	retryCmd.SetErr(&bytes.Buffer{})
	retryCmd.SetArgs([]string{"hook", "memory-extract-worker", "--job", launchedJobPath})
	if err := retryCmd.Execute(); err != nil {
		t.Fatalf("retry worker Execute() error = %v", err)
	}
	if _, err := os.Stat(launchedJobPath); !os.IsNotExist(err) {
		t.Fatalf("successful retry must remove job: %v", err)
	}
}

func TestRootCLI_HookSessionCommand_CodexStopKeepsSessionOpen(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "codex-stop-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-codex-stop-key"), []byte("codex-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-codex-stop-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	sessionStub := &sessionUsecaseStub{}
	memoryStub := &memoryUsecaseStub{
		extractErr: errors.New("simulated extract failure"),
	}
	var launchedJobPath string

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithSession(sessionStub),
		cli.WithMemory(memoryStub),
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			launchedJobPath = path
			return nil
		}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"codex-session"}`))
	rootCmd.SetArgs([]string{"hook", "session", "codex", "stop"})

	// Codex fires Stop after every assistant response (#1170): the stop
	// action is a turn boundary, so even an extract failure must leave
	// the session open and the hook state intact.
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := sessionStub.endCall.sessionID; got != "" {
		t.Fatalf("session.End called with %q, want no End on codex stop", got)
	}
	job := readHookMemoryExtractJobForTest(t, launchedJobPath)
	if got, want := job.SessionID, "codex-session"; got != want {
		t.Fatalf("queued session ID = %q, want %q", got, want)
	}
	if got := memoryStub.extractCriteria.SessionID(); got != "" {
		t.Fatalf("synchronous extraction session ID = %q, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "codex-codex-stop-key")); err != nil {
		t.Fatalf("session state should survive codex stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "codex-codex-stop-key-repo")); err != nil {
		t.Fatalf("workspace state should survive codex stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "ended", "codex-codex-session")); !os.IsNotExist(err) {
		t.Fatalf("codex stop must not create an end marker: %v", err)
	}
}

func TestRootCLI_HookSessionCommand_RepeatedStopsCoalesceMemoryExtractionJob(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "coalesce-key")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-coalesce-key"), []byte("coalesce-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-coalesce-key-repo"), []byte("traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	launched := []string{}
	for range 2 {
		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithSession(&sessionUsecaseStub{}),
			cli.WithMemory(&memoryUsecaseStub{}),
			cli.WithHookMemoryExtractLauncher(func(path string) error {
				launched = append(launched, path)
				return nil
			}),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetIn(strings.NewReader(`{"session_id":"coalesce-session"}`))
		rootCmd.SetArgs([]string{"hook", "session", "codex", "stop"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}
	if len(launched) != 2 || launched[0] != launched[1] {
		t.Fatalf("launched jobs = %v, want same coalesced path twice", launched)
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "memory-extract"))
	if err != nil {
		t.Fatalf("ReadDir(memory-extract) error = %v", err)
	}
	jsonCount := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			jsonCount++
		}
	}
	if jsonCount != 1 {
		t.Fatalf("memory extraction JSON jobs = %d, want 1", jsonCount)
	}
}

func TestRootCLI_HookMemoryExtractWorkerHandsOffRerunAtRunLimit(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "rerun-limit-key")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-rerun-limit-key"), []byte("rerun-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-rerun-limit-key-repo"), []byte("traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	var jobPath string
	queueCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			jobPath = path
			return nil
		}),
	).Command()
	queueCmd.SetOut(&bytes.Buffer{})
	queueCmd.SetErr(&bytes.Buffer{})
	queueCmd.SetIn(strings.NewReader(`{"session_id":"rerun-session"}`))
	queueCmd.SetArgs([]string{"hook", "session", "codex", "stop"})
	if err := queueCmd.Execute(); err != nil {
		t.Fatalf("queue Execute() error = %v", err)
	}

	memoryStub := &memoryUsecaseStub{}
	memoryStub.extractFunc = func(context.Context, apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error) {
		if err := os.WriteFile(jobPath+".rerun", []byte("rerun\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(rerun marker) error = %v", err)
		}
		return nil, nil
	}
	var handedOffPath string
	workerCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			handedOffPath = path
			return nil
		}),
	).Command()
	workerCmd.SetOut(&bytes.Buffer{})
	workerCmd.SetErr(&bytes.Buffer{})
	workerCmd.SetArgs([]string{"hook", "memory-extract-worker", "--job", jobPath})
	if err := workerCmd.Execute(); err != nil {
		t.Fatalf("worker Execute() error = %v", err)
	}
	if memoryStub.extractCallCount != 2 {
		t.Fatalf("extract calls = %d, want bounded 2", memoryStub.extractCallCount)
	}
	if handedOffPath != jobPath {
		t.Fatalf("handoff path = %q, want %q", handedOffPath, jobPath)
	}
	if _, err := os.Stat(jobPath); err != nil {
		t.Fatalf("handed-off job must remain pending: %v", err)
	}
}

func TestRootCLI_HookMemoryExtractWorkerHandsOffRerunPublishedBeforeRemoval(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "completion-race-key")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-completion-race-key"), []byte("completion-race-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-completion-race-key-repo"), []byte("traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	var jobPath string
	queueCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			jobPath = path
			return nil
		}),
	).Command()
	queueCmd.SetOut(&bytes.Buffer{})
	queueCmd.SetErr(&bytes.Buffer{})
	queueCmd.SetIn(strings.NewReader(`{"session_id":"completion-race-session"}`))
	queueCmd.SetArgs([]string{"hook", "session", "codex", "stop"})
	if err := queueCmd.Execute(); err != nil {
		t.Fatalf("queue Execute() error = %v", err)
	}

	var handedOffPath string
	workerCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
		cli.WithHookMemoryBeforeJobRemoval(func() {
			if err := os.WriteFile(jobPath+".rerun", []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile(rerun marker) error = %v", err)
			}
		}),
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			handedOffPath = path
			return nil
		}),
	).Command()
	workerCmd.SetOut(&bytes.Buffer{})
	workerCmd.SetErr(&bytes.Buffer{})
	workerCmd.SetArgs([]string{"hook", "memory-extract-worker", "--job", jobPath})
	if err := workerCmd.Execute(); err != nil {
		t.Fatalf("worker Execute() error = %v", err)
	}
	if handedOffPath != jobPath {
		t.Fatalf("handoff path = %q, want %q", handedOffPath, jobPath)
	}
	if _, err := os.Stat(jobPath); err != nil {
		t.Fatalf("completion-race job must be recreated: %v", err)
	}
}

func TestRootCLI_HookMemoryExtractWorkerHandsOffEnqueueAfterFinalCheck(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "post-check-race-key")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-post-check-race-key"), []byte("post-check-race-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-post-check-race-key-repo"), []byte("traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	var jobPath string
	queueCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			jobPath = path
			return nil
		}),
	).Command()
	queueCmd.SetOut(&bytes.Buffer{})
	queueCmd.SetErr(&bytes.Buffer{})
	queueCmd.SetIn(strings.NewReader(`{"session_id":"post-check-race-session"}`))
	queueCmd.SetArgs([]string{"hook", "session", "codex", "stop"})
	if err := queueCmd.Execute(); err != nil {
		t.Fatalf("queue Execute() error = %v", err)
	}

	contendingLaunches := 0
	handoffLaunches := 0
	workerCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
		cli.WithHookMemoryAfterFinalCheck(func() {
			contendingCmd := newTestRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithSession(&sessionUsecaseStub{}),
				cli.WithMemory(&memoryUsecaseStub{}),
				cli.WithHookMemoryExtractLauncher(func(string) error {
					contendingLaunches++
					return nil
				}),
			).Command()
			contendingCmd.SetOut(&bytes.Buffer{})
			contendingCmd.SetErr(&bytes.Buffer{})
			contendingCmd.SetIn(strings.NewReader(`{"session_id":"post-check-race-session"}`))
			contendingCmd.SetArgs([]string{"hook", "session", "codex", "stop"})
			if err := contendingCmd.Execute(); err != nil {
				t.Fatalf("contending Execute() error = %v", err)
			}
		}),
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			if path != jobPath {
				t.Fatalf("handoff path = %q, want %q", path, jobPath)
			}
			handoffLaunches++
			return nil
		}),
	).Command()
	workerCmd.SetOut(&bytes.Buffer{})
	workerCmd.SetErr(&bytes.Buffer{})
	workerCmd.SetArgs([]string{"hook", "memory-extract-worker", "--job", jobPath})
	if err := workerCmd.Execute(); err != nil {
		t.Fatalf("worker Execute() error = %v", err)
	}
	if contendingLaunches != 1 || handoffLaunches != 1 {
		t.Fatalf("launches = contending:%d handoff:%d, want 1 each", contendingLaunches, handoffLaunches)
	}
	if _, err := os.Stat(jobPath); err != nil {
		t.Fatalf("post-check race job must remain pending: %v", err)
	}
}

func TestRootCLI_HookMemoryExtractWorkerRejectsJobOutsideQueue(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	outsidePath := filepath.Join(t.TempDir(), strings.Repeat("a", 64)+".json")
	if err := os.WriteFile(outsidePath, []byte(`{"schema_version":1,"session_id":"outside"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(outside job) error = %v", err)
	}
	workerCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	).Command()
	workerCmd.SetOut(&bytes.Buffer{})
	workerCmd.SetErr(&bytes.Buffer{})
	workerCmd.SetArgs([]string{"hook", "memory-extract-worker", "--job", outsidePath})
	err := workerCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "outside the queue directory") {
		t.Fatalf("worker error = %v, want outside-queue rejection", err)
	}
}

// TestRootCLI_HookSessionCommand_CodexMultiTurnSessionStaysActive is the
// regression fixture for #1170. It models the Codex rollout JSONL shape
// (session_meta -> response_item/turn_context -> Stop transcript -> later
// turn_context/response_item) at the hook level with synthetic text:
// `SessionStart -> Stop/transcript -> Prompt -> PostToolUse` must keep one
// active session until a real session-end signal or stale GC closes it.
func TestRootCLI_HookSessionCommand_CodexMultiTurnSessionStaysActive(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "codex-multi-turn-key")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	t.Setenv("TRACEARY_DB_PATH", dbPath)
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)

	runHook := func(payload string, args ...string) {
		t.Helper()
		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithSession(sessionUC),
			cli.WithEvent(eventUC),
			cli.WithDatabasePathSetter(db.SetPath),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetIn(strings.NewReader(payload))
		rootCmd.SetArgs(args)
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
	}

	// Turn 1: session_meta -> assistant response -> Stop (transcript + stop).
	runHook(`{"session_id":"codex-multi-turn"}`, "hook", "session", "codex", "start")
	runHook(`{"session_id":"codex-multi-turn","last_assistant_message":"synthetic assistant reply"}`, "hook", "transcript", "codex")
	runHook(`{"session_id":"codex-multi-turn"}`, "hook", "session", "codex", "stop")
	// Turn 2 arrives after the Stop: the same session keeps receiving events.
	runHook(`{"session_id":"codex-multi-turn","prompt":"synthetic follow-up prompt"}`, "hook", "prompt", "codex")
	runHook(`{"session_id":"codex-multi-turn","tool_input":{"command":"echo synthetic"},"tool_response":{"exitCode":0,"stdout":"ok","stderr":""}}`, "hook", "audit", "codex")

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	var endedAt sql.NullString
	if err := sqlDB.QueryRow(`SELECT ended_at FROM sessions WHERE session_id = ?`, "codex-multi-turn").Scan(&endedAt); err != nil {
		t.Fatalf("query sessions.ended_at error = %v", err)
	}
	if endedAt.Valid {
		t.Fatalf("sessions.ended_at = %q, want NULL (stop must not close a multi-turn session)", endedAt.String)
	}

	wantKinds := map[string]int{
		"session_started":  1,
		"transcript":       1,
		"prompt":           1,
		"command_executed": 1,
		"session_ended":    0,
	}
	for kind, want := range wantKinds {
		var got int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM events WHERE session_id = ? AND kind = ?`, "codex-multi-turn", kind).Scan(&got); err != nil {
			t.Fatalf("query %s count error = %v", kind, err)
		}
		if got != want {
			t.Fatalf("%s events = %d, want %d", kind, got, want)
		}
	}

	// Active-session reads must include the ongoing session: the
	// row-based snapshot (sessions --snapshot) and the event-based
	// active read (session active) have to agree (#1170 acceptance).
	snapshotOut := &bytes.Buffer{}
	snapshotCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithSession(sessionUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	snapshotCmd.SetOut(snapshotOut)
	snapshotCmd.SetErr(&bytes.Buffer{})
	snapshotCmd.SetArgs([]string{"sessions", "--snapshot", "--json", "--db-path", dbPath, "--workspace", "github.com/duck8823/traceary"})
	if err := snapshotCmd.Execute(); err != nil {
		t.Fatalf("Execute(sessions --snapshot --json) error = %v", err)
	}
	if !strings.Contains(snapshotOut.String(), `"session_id": "codex-multi-turn"`) {
		t.Fatalf("sessions --snapshot should include the ongoing codex session, got: %s", snapshotOut.String())
	}

	activeOut := &bytes.Buffer{}
	activeCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithSession(sessionUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	activeCmd.SetOut(activeOut)
	activeCmd.SetErr(&bytes.Buffer{})
	activeCmd.SetArgs([]string{"session", "active", "--json", "--db-path", dbPath, "--workspace", "github.com/duck8823/traceary"})
	if err := activeCmd.Execute(); err != nil {
		t.Fatalf("Execute(session active --json) error = %v", err)
	}
	if !strings.Contains(activeOut.String(), "codex-multi-turn") {
		t.Fatalf("session active should return the ongoing codex session, got: %s", activeOut.String())
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
		wantReason types.CommandFailureReason
	}{
		{
			name:       "claude post-tool-use failure with top-level error",
			client:     "claude",
			payload:    `{"tool_input":{"command":"go test ./..."},"error":"Exit code 7\nFAIL","is_interrupt":false}`,
			wantFailed: true,
			wantReason: types.CommandFailureReasonHostError,
		},
		{
			name:       "claude success payload is not flagged",
			client:     "claude",
			payload:    `{"tool_input":{"command":"go test ./..."},"tool_response":{"interrupted":false,"stdout":"ok","stderr":""}}`,
			wantFailed: false,
			wantReason: types.CommandFailureReasonUnknown,
		},
		{
			name:       "gemini spawn error in tool_response.error",
			client:     "gemini",
			payload:    `{"tool_input":{"command":"missing-binary"},"tool_response":{"llmContent":"failed","error":{"type":"shell_execute_error","message":"spawn failed"}}}`,
			wantFailed: true,
			wantReason: types.CommandFailureReasonHostError,
		},
		{
			name:       "claude interrupt is a signal",
			client:     "claude",
			payload:    `{"tool_input":{"command":"go test ./..."},"error":"interrupted","is_interrupt":true}`,
			wantFailed: true,
			wantReason: types.CommandFailureReasonSignal,
		},
		{
			name:       "structured timeout is a timeout",
			client:     "codex",
			payload:    `{"tool_input":{"command":"go test ./..."},"error":"deadline","timed_out":true}`,
			wantFailed: true,
			wantReason: types.CommandFailureReasonTimeout,
		},
		{
			name:       "empty top-level error is not a failure",
			client:     "claude",
			payload:    `{"tool_input":{"command":"go test ./..."},"error":""}`,
			wantFailed: false,
			wantReason: types.CommandFailureReasonUnknown,
		},
		{
			name:       "gemini empty tool_response.error is not a failure",
			client:     "gemini",
			payload:    `{"tool_input":{"command":"go test ./..."},"tool_response":{"llmContent":"ok","error":""}}`,
			wantFailed: false,
			wantReason: types.CommandFailureReasonUnknown,
		},
		{
			name:       "codex raw-string tool_response mentioning error is not flagged",
			client:     "codex",
			payload:    `{"tool_input":{"command":"go test ./..."},"tool_response":"error: a test logged the word error but exited 0"}`,
			wantFailed: false,
			wantReason: types.CommandFailureReasonUnknown,
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
			if got := eventStub.auditCall.failureReason; got != tc.wantReason {
				t.Fatalf("audit failure reason = %q, want %q", got, tc.wantReason)
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

func TestRootCLI_HookCompactCommand_RecordsCodexPostCompactMarker(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-test-key"), []byte("codex-compact-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}
	eventStub := &eventUsecaseStub{logEvent: model.EventOf(types.EventID("evt-codex-post-compact"), types.EventKindCompactSummary, types.Client("hook"), types.Agent("codex"), types.SessionID("codex-compact-session"), types.Workspace("github.com/duck8823/traceary"), "auto", time.Now())}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithEvent(eventStub)).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"codex-compact-session","trigger":"auto"}`))
	rootCmd.SetArgs([]string{"hook", "compact", "codex", "post-compact"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(post-compact) error = %v", err)
	}
	if got, want := eventStub.logCall.kind, types.EventKindCompactSummary; got != want {
		t.Fatalf("post-compact log kind = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.message, "auto"; got != want {
		t.Fatalf("post-compact log message = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.sourceHook, "post_compact"; got != want {
		t.Fatalf("post-compact source_hook = %q, want %q", got, want)
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

func TestRootCLI_HookSubagentStartCommand_UsesCodexAgentFields(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "codex-start-key")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-codex-start-key"), []byte("codex-parent"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-codex-start-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}
	sessionStub := &sessionUsecaseStub{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithSession(sessionStub)).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"agent_id":"019-agent","agent_type":"reviewer"}`))
	rootCmd.SetArgs([]string{"hook", "subagent-start", "codex"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-start) error = %v", err)
	}
	if got, want := sessionStub.startChildCall.parent, types.SessionID("codex-parent"); got != want {
		t.Fatalf("StartChild parent = %q, want %q", got, want)
	}
	if got, want := sessionStub.startChildCall.childID, types.SessionID("codex-parent:sub:agent-019-agent"); got != want {
		t.Fatalf("StartChild childID = %q, want %q", got, want)
	}
	if got, want := sessionStub.startChildCall.agent, types.Agent("codex/reviewer"); got != want {
		t.Fatalf("StartChild agent = %q, want %q", got, want)
	}
}

func TestRootCLI_HookSubagentCommands_CorrelateCodexAgentID(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "codex-lifecycle-key")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-codex-lifecycle-key"), []byte("codex-parent"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "codex-codex-lifecycle-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}
	sessionStub := &sessionUsecaseStub{}
	eventStub := &eventUsecaseStub{}
	newCommand := func(payload string, args ...string) *cobra.Command {
		cmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithSession(sessionStub), cli.WithEvent(eventStub)).Command()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader(payload))
		cmd.SetArgs(args)
		return cmd
	}
	payload := `{"session_id":"codex-parent","agent_id":"019-agent","agent_type":"reviewer"}`
	if err := newCommand(payload, "hook", "subagent-start", "codex").Execute(); err != nil {
		t.Fatalf("Execute(subagent-start) error = %v", err)
	}
	if err := newCommand(payload, "hook", "subagent-stop", "codex").Execute(); err != nil {
		t.Fatalf("Execute(subagent-stop) error = %v", err)
	}
	if got, want := sessionStub.endCall.sessionID, types.SessionID("codex-parent:sub:agent-019-agent"); got != want {
		t.Fatalf("End child sessionID = %q, want %q", got, want)
	}
	activePath := filepath.Join(stateDir, "active-subagents", "codex-codex-parent")
	if data, err := os.ReadFile(activePath); err == nil && strings.Contains(string(data), "agent-019-agent") {
		t.Fatalf("active state still contains stopped Codex agent: %s", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadFile(active state) error = %v", err)
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
	if err := os.WriteFile(filepath.Join(stateDir, "claude-stop-child-key-repo"), []byte("traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}
	startedAt := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(activeDir, "claude-parent-session"), []byte(`{"children":{"toolu_1":{"child_session_id":"parent-session:sub:toolu_1","started_at":"`+startedAt+`"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(active state) error = %v", err)
	}
	sessionStub := &sessionUsecaseStub{}
	eventStub := &eventUsecaseStub{}
	memoryStub := &memoryUsecaseStub{}
	var launchedJobPath string
	primaryEventCompleteAtLaunch := false
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithEvent(eventStub),
		cli.WithMemory(memoryStub),
		cli.WithHookMemoryExtractLauncher(func(path string) error {
			launchedJobPath = path
			primaryEventCompleteAtLaunch = eventStub.logCall.sessionID == types.SessionID("parent-session")
			return nil
		}),
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
	if !primaryEventCompleteAtLaunch {
		t.Fatal("worker launched before subagent-stop primary event completed")
	}
	job := readHookMemoryExtractJobForTest(t, launchedJobPath)
	if got, want := job.SessionID, "parent-session:sub:toolu_1"; got != want {
		t.Fatalf("queued subagent session ID = %q, want %q", got, want)
	}
	if got, want := job.Workspace, "traceary"; got != want {
		t.Fatalf("queued subagent workspace = %q, want %q", got, want)
	}
	if got, want := job.SourceBoundary, "subagent_stop"; got != want {
		t.Fatalf("queued source boundary = %q, want %q", got, want)
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
	// No transcript_path and no last_assistant_message → no event recorded, no failure.
	if eventStub.logCall.message != "" {
		t.Fatalf("logCall.message = %q, want empty when transcript_path missing", eventStub.logCall.message)
	}
}

// TestRootCLI_HookTranscriptCommand_ClaudeFallsBackToLastAssistantMessage
// pins the print-mode race fix (#1307): Stop can fire before the JSONL
// assistant row is flushed. Prefer last_assistant_message when the file
// has no usable blocks so non-interactive claude -p still records one
// transcript.
func TestRootCLI_HookTranscriptCommand_ClaudeFallsBackToLastAssistantMessage(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key-transcript-print-race")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-transcript-print-race"), []byte("print-session"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-transcript-print-race-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	// Empty / incomplete JSONL: only a user turn, no assistant row yet.
	transcriptPath := filepath.Join(t.TempDir(), "race-transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"user","message":{"role":"user","content":"say PASS"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(transcript) error = %v", err)
	}

	storeStub := &storeManagementUsecaseStub{}
	eventStub := &eventUsecaseStub{
		logEvent: model.EventOf(
			types.EventID("evt-print-transcript"),
			types.EventKindTranscript,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("print-session"),
			types.Workspace("github.com/duck8823/traceary"),
			"PASS",
			time.Now(),
		),
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeStub),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	payload := `{"session_id":"print-session","transcript_path":"` + transcriptPath + `","last_assistant_message":"PASS"}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "transcript", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
		t.Fatalf("transcript log kind = %q, want %q", got, want)
	}
	gotBlocks := apptypes.ParseEventBodyBlocks(eventStub.logCall.message)
	wantBlocks := []apptypes.EventBodyBlock{
		{Type: apptypes.EventBodyBlockTypeText, Text: "PASS"},
	}
	if diff := cmp.Diff(wantBlocks, gotBlocks); diff != "" {
		t.Fatalf("transcript blocks mismatch (-want +got):\n%s", diff)
	}
}

// TestRootCLI_HookTranscriptCommand_ClaudeDoesNotFabricateFromEmptyLastMessage
// ensures error/quota exits that supply blank last_assistant_message and an
// incomplete JSONL do not invent a successful transcript.
func TestRootCLI_HookTranscriptCommand_ClaudeDoesNotFabricateFromEmptyLastMessage(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	transcriptPath := filepath.Join(t.TempDir(), "empty-race.jsonl")
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(transcript) error = %v", err)
	}

	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	payload := `{"session_id":"empty-session","transcript_path":"` + transcriptPath + `","last_assistant_message":""}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "transcript", "claude"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if eventStub.logCall.message != "" {
		t.Fatalf("logCall.message = %q, want empty when last_assistant_message is blank", eventStub.logCall.message)
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

func TestRootCLI_HookPromptCommand_PrefersExplicitWorkspaceOverPersistedState(t *testing.T) {
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
	if got, want := eventStub.logCall.workspace, types.Workspace("env/workspace"); got != want {
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
