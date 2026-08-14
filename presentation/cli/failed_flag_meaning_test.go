package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_ListFailuresKeepsStructuredAndLegacyFailedRows pins #1767:
// list --failures stays, current hook writes persist host_error (not
// unknown+failed), and restored pre-classifier unknown+failed rows still match.
func TestRootCLI_ListFailuresKeepsStructuredAndLegacyFailedRows(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "test-key")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key"), []byte("session-failed-flag"), 0o600); err != nil {
		t.Fatalf("WriteFile(session state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "claude-test-key-repo"), []byte("github.com/duck8823/traceary"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace state) error = %v", err)
	}

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	hookCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithEvent(eventUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	hookCmd.SetOut(&bytes.Buffer{})
	hookCmd.SetErr(&bytes.Buffer{})
	hookCmd.SetIn(strings.NewReader(`{"tool_input":{"command":"go test ./..."},"error":"Exit code 7\nFAIL","is_interrupt":false}`))
	hookCmd.SetArgs([]string{"hook", "audit", "claude", "--db-path", dbPath})
	if err := hookCmd.Execute(); err != nil {
		t.Fatalf("hook audit Execute() error = %v", err)
	}

	legacyAudit, err := model.CommandAuditFromSnapshot(model.CommandAuditSnapshot{
		EventID:       "legacy-unknown-failed",
		Command:       "legacy tool without classifier",
		CommandName:   types.CommandNameUnknown,
		Failed:        true,
		FailureReason: types.CommandFailureReasonUnknown,
	})
	if err != nil {
		t.Fatalf("CommandAuditFromSnapshot() error = %v", err)
	}
	legacyEvent := model.EventOf(
		types.EventID("legacy-unknown-failed"),
		types.EventKindCommandExecuted,
		types.Client("hook"),
		types.Agent("claude"),
		types.SessionID("session-failed-flag"),
		types.Workspace("github.com/duck8823/traceary"),
		"legacy tool without classifier",
		time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	)
	if err := eventDS.SaveWithAudit(ctx, legacyEvent, legacyAudit); err != nil {
		t.Fatalf("SaveWithAudit(legacy) error = %v", err)
	}

	successAudit, err := model.NewCommandAudit("success-ok", "echo ok", "", "ok", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit(success) error = %v", err)
	}
	if err := successAudit.ClassifyOutcome(types.Some(0), types.CommandFailureReasonNone, false); err != nil {
		t.Fatalf("ClassifyOutcome(success) error = %v", err)
	}
	successEvent := model.EventOf(
		types.EventID("success-ok"),
		types.EventKindCommandExecuted,
		types.Client("hook"),
		types.Agent("claude"),
		types.SessionID("session-failed-flag"),
		types.Workspace("github.com/duck8823/traceary"),
		"echo ok",
		time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	)
	if err := eventDS.SaveWithAudit(ctx, successEvent, successAudit); err != nil {
		t.Fatalf("SaveWithAudit(success) error = %v", err)
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	var hookFailed int
	var hookReason string
	if err := sqlDB.QueryRowContext(
		ctx,
		`SELECT failed, failure_reason FROM command_audits WHERE command_text LIKE '%go test%'`,
	).Scan(&hookFailed, &hookReason); err != nil {
		t.Fatalf("query hook audit error = %v", err)
	}
	if hookFailed != 1 || hookReason != types.CommandFailureReasonHostError.String() {
		t.Fatalf("current hook write = failed:%d reason:%q, want failed=1 host_error", hookFailed, hookReason)
	}

	var legacyFailed int
	var legacyReason string
	if err := sqlDB.QueryRowContext(
		ctx,
		`SELECT failed, failure_reason FROM command_audits WHERE event_id = 'legacy-unknown-failed'`,
	).Scan(&legacyFailed, &legacyReason); err != nil {
		t.Fatalf("query legacy audit error = %v", err)
	}
	if legacyFailed != 1 || legacyReason != types.CommandFailureReasonUnknown.String() {
		t.Fatalf("legacy restore = failed:%d reason:%q, want failed=1 unknown", legacyFailed, legacyReason)
	}

	stdout := &bytes.Buffer{}
	listCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithEvent(eventUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	listCmd.SetOut(stdout)
	listCmd.SetErr(&bytes.Buffer{})
	listCmd.SetArgs([]string{"list", "--failures", "--json", "--db-path", dbPath})
	if err := listCmd.Execute(); err != nil {
		t.Fatalf("list --failures Execute() error = %v", err)
	}

	var listed []struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("list --failures JSON = %s, unmarshal error = %v", stdout.String(), err)
	}
	got := map[string]bool{}
	for _, row := range listed {
		got[row.EventID] = true
	}
	if !got["legacy-unknown-failed"] {
		t.Fatalf("list --failures omitted restored unknown+failed row; got %v", got)
	}
	if got["success-ok"] {
		t.Fatalf("list --failures included success row; got %v", got)
	}
	hookListed := false
	for id := range got {
		if id != "legacy-unknown-failed" {
			hookListed = true
		}
	}
	if !hookListed {
		t.Fatalf("list --failures omitted current host_error write; got %v", got)
	}
}
