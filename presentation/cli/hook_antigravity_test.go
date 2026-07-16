package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"

	_ "modernc.org/sqlite"
)

// runAntigravityHook executes a hidden `hook antigravity <event>` subcommand
// with the given payload and returns the JSON the runtime wrote to stdout. The
// runtime entrypoints are fail-soft, so Execute() is expected to succeed and
// the test asserts the output contract plus any recorded usecase calls.
func runAntigravityHook(t *testing.T, event, payload string, opts ...cli.RootCLIOption) (string, *eventUsecaseStub, *sessionUsecaseStub) {
	t.Helper()

	eventStub := &eventUsecaseStub{}
	sessionStub := &sessionUsecaseStub{}
	base := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	}
	rootCmd := newTestRootCLI(append(base, opts...)...).Command()

	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "antigravity", event})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(hook antigravity %s) error = %v", event, err)
	}
	return stdout.String(), eventStub, sessionStub
}

func TestRootCLI_HookAntigravityPreInvocation(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	out, _, sessionStub := runAntigravityHook(t, "pre-invocation",
		`{"conversationId":"conv-1","workspacePaths":["/repo"]}`)

	if out != "{}" {
		t.Fatalf("PreInvocation output = %q, want {}", out)
	}
	if got, want := sessionStub.startCall.sessionID, types.SessionID("conv-1"); got != want {
		t.Fatalf("session start sessionID = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.agent, types.Agent("antigravity"); got != want {
		t.Fatalf("session start agent = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.workspace, types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Fatalf("session start workspace = %q, want %q", got, want)
	}
}

func TestRootCLI_HookAntigravityPreInvocationMissingConversationIsNoop(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	out, _, sessionStub := runAntigravityHook(t, "pre-invocation", `{"workspacePaths":["/repo"]}`)

	if out != "{}" {
		t.Fatalf("PreInvocation output = %q, want {}", out)
	}
	if sessionStub.startCall.sessionID != "" {
		t.Fatalf("session should not start without conversationId, got %q", sessionStub.startCall.sessionID)
	}
}

func TestRootCLI_HookAntigravityPreInvocationProtectsCurrentSessionFromGC(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "antigravity-gc-protection")
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
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
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	opts := []cli.RootCLIOption{
		cli.WithStoreManagement(storeUC),
		cli.WithSession(sessionUC),
		cli.WithDatabasePathSetter(db.SetPath),
	}
	payload := `{"conversationId":"conv-current","workspacePaths":["/repo"]}`
	if out, _, _ := runAntigravityHook(t, "pre-invocation", payload, opts...); out != "{}" {
		t.Fatalf("initial PreInvocation output = %q, want {}", out)
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`UPDATE sessions SET started_at = ? WHERE session_id = 'conv-current'`, old); err != nil {
		t.Fatalf("age current session: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE events SET created_at = ? WHERE session_id = 'conv-current'`, old); err != nil {
		t.Fatalf("age current events: %v", err)
	}
	markers, err := filepath.Glob(filepath.Join(os.Getenv("TRACEARY_HOOK_STATE_DIR"), "session-gc", "*.stamp"))
	if err != nil || len(markers) != 1 {
		t.Fatalf("session GC markers = %v, err = %v, want one", markers, err)
	}
	markerExpired := time.Now().Add(-7 * time.Hour)
	if err := os.Chtimes(markers[0], markerExpired, markerExpired); err != nil {
		t.Fatalf("expire GC marker: %v", err)
	}

	if out, _, _ := runAntigravityHook(t, "pre-invocation", payload, opts...); out != "{}" {
		t.Fatalf("current PreInvocation output = %q, want {}", out)
	}
	var endedAt sql.NullString
	if err := sqlDB.QueryRow(`SELECT ended_at FROM sessions WHERE session_id = 'conv-current'`).Scan(&endedAt); err != nil {
		t.Fatalf("select current ended_at: %v", err)
	}
	if endedAt.Valid {
		t.Fatalf("current Antigravity session was closed at %s", endedAt.String)
	}
}

func TestRootCLI_HookAntigravityConcurrentPreInvocationsProtectAllActiveSessions(t *testing.T) {
	if os.Getenv("TRACEARY_ANTIGRAVITY_GC_HELPER") == "1" {
		dbPath := os.Getenv("TRACEARY_DB_PATH")
		db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
		eventDS := sqliteinfra.NewEventDatasource(db)
		sessionDS := sqliteinfra.NewSessionDatasource(db)
		storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
		sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithSession(sessionUC),
			cli.WithDatabasePathSetter(db.SetPath),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetIn(strings.NewReader(os.Getenv("TRACEARY_ANTIGRAVITY_GC_PAYLOAD")))
		rootCmd.SetArgs([]string{"hook", "antigravity", "pre-invocation"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("helper PreInvocation error = %v", err)
		}
		return
	}

	stateDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	if err := storeUC.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	for _, sessionID := range []string{"conv-a", "conv-b", "abandoned"} {
		if _, err := sqlDB.Exec(`INSERT INTO sessions(session_id, started_at) VALUES (?, ?)`, sessionID, old); err != nil {
			t.Fatalf("insert %s: %v", sessionID, err)
		}
	}
	routingStates := map[string]string{"key-a": "conv-a", "key-b": "conv-b", "abandoned-key": "abandoned"}
	for key, sessionID := range routingStates {
		path := filepath.Join(stateDir, "antigravity-"+key)
		if err := os.WriteFile(path, []byte(sessionID), 0o600); err != nil {
			t.Fatalf("write %s state: %v", sessionID, err)
		}
		if sessionID == "abandoned" {
			if err := os.Chtimes(path, time.Now().Add(-96*time.Hour), time.Now().Add(-96*time.Hour)); err != nil {
				t.Fatalf("age abandoned routing state: %v", err)
			}
		}
	}
	activityDir := filepath.Join(stateDir, "session-activity")
	if err := os.MkdirAll(activityDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(activityDir) error = %v", err)
	}
	for _, sessionID := range []string{"conv-a", "conv-b", "abandoned"} {
		digest := sha256.Sum256([]byte(sessionID))
		path := filepath.Join(activityDir, hex.EncodeToString(digest[:])+".lease")
		if err := os.WriteFile(path, []byte(sessionID), 0o600); err != nil {
			t.Fatalf("write %s activity lease: %v", sessionID, err)
		}
		if sessionID == "abandoned" {
			if err := os.Chtimes(path, time.Now().Add(-10*time.Minute), time.Now().Add(-10*time.Minute)); err != nil {
				t.Fatalf("age abandoned activity lease: %v", err)
			}
		}
	}

	type helperProcess struct {
		cmd    *exec.Cmd
		output *bytes.Buffer
	}
	commands := make([]helperProcess, 0, 2)
	for key, sessionID := range map[string]string{"key-a": "conv-a", "key-b": "conv-b"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRootCLI_HookAntigravityConcurrentPreInvocationsProtectAllActiveSessions$")
		cmd.Env = append(os.Environ(),
			"TRACEARY_ANTIGRAVITY_GC_HELPER=1",
			"TRACEARY_ANTIGRAVITY_GC_PAYLOAD={\"conversationId\":\""+sessionID+"\",\"workspacePaths\":[\"/repo\"]}",
			"TRACEARY_HOOK_STATE_DIR="+stateDir,
			"TRACEARY_HOOK_STATE_KEY="+key,
			"TRACEARY_DB_PATH="+dbPath,
			"TRACEARY_WORKSPACE=github.com/duck8823/traceary",
		)
		output := &bytes.Buffer{}
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %s helper: %v", sessionID, err)
		}
		commands = append(commands, helperProcess{cmd: cmd, output: output})
	}
	for _, helper := range commands {
		if err := helper.cmd.Wait(); err != nil {
			t.Fatalf("helper error = %v\n%s", err, helper.output.Bytes())
		}
	}
	abandonedDigest := sha256.Sum256([]byte("abandoned"))
	abandonedLeasePath := filepath.Join(activityDir, hex.EncodeToString(abandonedDigest[:])+".lease")
	if _, err := os.Stat(abandonedLeasePath); !os.IsNotExist(err) {
		t.Fatalf("expired abandoned activity lease must be pruned, stat error = %v", err)
	}

	for _, tc := range []struct {
		sessionID  string
		wantClosed bool
	}{
		{sessionID: "conv-a", wantClosed: false},
		{sessionID: "conv-b", wantClosed: false},
		{sessionID: "abandoned", wantClosed: true},
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

func TestRootCLI_HookAntigravityToolUsePairing(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// PreToolUse persists the run_command details keyed by conversationId+stepIdx.
	preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"conv-1","stepIdx":"3","toolCall":{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/repo"}}}`)
	if preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want {\"decision\":\"allow\"}", preOut)
	}

	// PostToolUse — carrying only stepIdx/error — pairs the persisted command
	// and records a command audit.
	postOut, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-1","stepIdx":"3","error":""}`)
	if postOut != "{}" {
		t.Fatalf("PostToolUse output = %q, want {}", postOut)
	}
	if got, want := eventStub.auditCall.command, "go test ./..."; got != want {
		t.Fatalf("audit command = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.agent, types.Agent("antigravity"); got != want {
		t.Fatalf("audit agent = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.sessionID, types.SessionID("conv-1"); got != want {
		t.Fatalf("audit sessionID = %q, want %q", got, want)
	}
	if eventStub.auditCall.failed {
		t.Fatalf("audit should not be flagged failed for an empty error field")
	}
}

func TestRootCLI_HookAntigravityToolUsePairingNumericStepIdx(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// Antigravity may emit stepIdx as a JSON number rather than a string. Both
	// Pre/PostToolUse key pending state on the same rendered value, so the
	// numeric form still pairs and records an audit.
	if preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"conv-num","stepIdx":5,"toolCall":{"name":"run_command","args":{"CommandLine":"go vet ./...","Cwd":"/repo"}}}`); preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want allow", preOut)
	}

	_, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-num","stepIdx":5,"error":""}`)
	if got, want := eventStub.auditCall.command, "go vet ./..."; got != want {
		t.Fatalf("audit command = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.sessionID, types.SessionID("conv-num"); got != want {
		t.Fatalf("audit sessionID = %q, want %q", got, want)
	}
}

func TestRootCLI_HookAntigravityPrunesStalePendingCommands(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	cli.SetAntigravityPendingNowFunc(func() time.Time { return now })
	t.Cleanup(cli.ResetAntigravityPendingNowFunc)

	// A PreToolUse persists a pending command for a step that never receives a
	// matching PostToolUse.
	if preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"stale-conv","stepIdx":"1","toolCall":{"name":"run_command","args":{"CommandLine":"sleep 1","Cwd":"/repo"}}}`); preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want allow", preOut)
	}

	stalePath, err := cli.AntigravityPendingCommandPath("stale-conv", "1")
	if err != nil {
		t.Fatalf("AntigravityPendingCommandPath error = %v", err)
	}
	// Age the unpaired file beyond the TTL relative to the pinned clock.
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatalf("Chtimes error = %v", err)
	}

	// A later PreToolUse for a different step opportunistically prunes the stale
	// sibling while persisting its own fresh state.
	if preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"fresh-conv","stepIdx":"1","toolCall":{"name":"run_command","args":{"CommandLine":"go build ./...","Cwd":"/repo"}}}`); preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want allow", preOut)
	}

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale pending command should be pruned, stat err = %v", err)
	}
	freshPath, err := cli.AntigravityPendingCommandPath("fresh-conv", "1")
	if err != nil {
		t.Fatalf("AntigravityPendingCommandPath error = %v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("fresh pending command should persist, stat err = %v", err)
	}
}

func TestRootCLI_HookAntigravityToolUsePairingFlagsError(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	if preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"conv-2","stepIdx":"7","toolCall":{"name":"run_command","args":{"CommandLine":"false","Cwd":"/repo"}}}`); preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want allow", preOut)
	}

	_, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-2","stepIdx":"7","error":"command exited with status 1"}`)
	if !eventStub.auditCall.failed {
		t.Fatalf("audit should be flagged failed when PostToolUse carries an error")
	}
}

func TestRootCLI_HookAntigravityPostToolUseWithoutPendingIsNoop(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// No PreToolUse ran for this step, so there is no pending command to pair.
	out, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-3","stepIdx":"1","error":""}`)
	if out != "{}" {
		t.Fatalf("PostToolUse output = %q, want {}", out)
	}
	if eventStub.auditCall.command != "" {
		t.Fatalf("PostToolUse without pending state must not record an audit, got command %q", eventStub.auditCall.command)
	}
}

func TestRootCLI_HookAntigravityPreToolUseNonRunCommandIsNoop(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// A non-run_command tool persists nothing; a following PostToolUse for the
	// same step therefore records no audit.
	preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"conv-4","stepIdx":"2","toolCall":{"name":"read_file","args":{"Path":"README.md"}}}`)
	if preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want allow", preOut)
	}

	out, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-4","stepIdx":"2","error":""}`)
	if out != "{}" {
		t.Fatalf("PostToolUse output = %q, want {}", out)
	}
	if eventStub.auditCall.command != "" {
		t.Fatalf("non-run_command tool must not record an audit, got command %q", eventStub.auditCall.command)
	}
}

func TestRootCLI_HookAntigravityStopOutputContract(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	out, _, _ := runAntigravityHook(t, "stop",
		`{"conversationId":"conv-1","workspacePaths":["/repo"],"terminationReason":"completed"}`)
	if out != `{"decision":""}` {
		t.Fatalf("Stop output = %q, want {\"decision\":\"\"}", out)
	}
}

func TestRootCLI_HookAntigravityPreInvocationEmptyWorkspacePathsUsesParentCwd(t *testing.T) {
	// Live agy 1.1.x payloads can emit workspacePaths:[] while still firing
	// PreInvocation. Recover the project from the host process cwd chain.
	t.Setenv("TRACEARY_WORKSPACE", "")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	projectDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(projectDir, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	// Seed git metadata so detectRepoContextFromDir can produce a stable local
	// workspace identity from the recovered cwd.
	if err := os.WriteFile(filepath.Join(projectDir, ".git", "config"), []byte("[core]\n\trepositoryformatversion = 0\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(git config) error = %v", err)
	}

	cli.SetAntigravityParentPIDFunc(func() int { return 4242 })
	t.Cleanup(cli.ResetAntigravityParentPIDFunc)
	cli.SetAntigravityProcessCwdFunc(func(pid int) (string, error) {
		if pid == 4242 {
			return projectDir, nil
		}
		return "", os.ErrNotExist
	})
	t.Cleanup(cli.ResetAntigravityProcessCwdFunc)

	out, _, sessionStub := runAntigravityHook(t, "pre-invocation",
		`{"conversationId":"conv-empty-ws","workspacePaths":[]}`)
	if out != "{}" {
		t.Fatalf("PreInvocation output = %q, want {}", out)
	}
	if got, want := sessionStub.startCall.sessionID, types.SessionID("conv-empty-ws"); got != want {
		t.Fatalf("session start sessionID = %q, want %q", got, want)
	}
	if sessionStub.startCall.workspace == "" {
		t.Fatalf("session start workspace is empty; parent cwd fallback did not bind a workspace")
	}
}

func TestAntigravityWorkspaceCwdPrefersPayloadThenParent(t *testing.T) {
	if got := cli.AntigravityWorkspaceCwd([]byte(`{"workspacePaths":["/from-payload"]}`)); got != "/from-payload" {
		t.Fatalf("payload path = %q, want /from-payload", got)
	}

	cli.SetAntigravityParentPIDFunc(func() int { return 99 })
	t.Cleanup(cli.ResetAntigravityParentPIDFunc)
	cli.SetAntigravityProcessCwdFunc(func(pid int) (string, error) {
		if pid == 99 {
			return "/recovered/project", nil
		}
		return "", os.ErrNotExist
	})
	t.Cleanup(cli.ResetAntigravityProcessCwdFunc)

	if got := cli.AntigravityWorkspaceCwd([]byte(`{"workspacePaths":[]}`)); got != "/recovered/project" {
		t.Fatalf("empty workspacePaths fallback = %q, want /recovered/project", got)
	}
	if got := cli.AntigravityWorkspaceCwd([]byte(`{}`)); got != "/recovered/project" {
		t.Fatalf("missing workspacePaths fallback = %q, want /recovered/project", got)
	}
}

func TestIsAntigravityNonWorkspaceCwd(t *testing.T) {
	// Exercise through AntigravityWorkspaceCwd by feeding a config-dir parent
	// cwd and ensuring it is rejected when no payload path is present.
	cli.SetAntigravityParentPIDFunc(func() int { return 77 })
	t.Cleanup(cli.ResetAntigravityParentPIDFunc)
	cli.SetAntigravityProcessCwdFunc(func(pid int) (string, error) {
		if pid == 77 {
			return filepath.Join("/Users/me", ".gemini", "config", "plugins", "traceary"), nil
		}
		return "", os.ErrNotExist
	})
	t.Cleanup(cli.ResetAntigravityProcessCwdFunc)
	if got := cli.AntigravityWorkspaceCwd([]byte(`{"workspacePaths":[]}`)); got != "" {
		t.Fatalf("config plugin cwd should be rejected, got %q", got)
	}
}
