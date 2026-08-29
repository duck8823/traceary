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

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

func newHookAuditStore(t *testing.T) (dbPath string, eventUC usecase.EventUsecase, storeUC usecase.StoreManagementUsecase, setPath func(string)) {
	t.Helper()
	ctx := context.Background()
	dbPath = filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	storeUC = usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	eventUC = usecase.NewEventUsecase(eventDS, eventDS)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return dbPath, eventUC, storeUC, db.SetPath
}

func executeHookAudit(t *testing.T, dbPath string, eventUC usecase.EventUsecase, storeUC usecase.StoreManagementUsecase, setPath func(string), args []string, payload string) {
	t.Helper()
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithEvent(eventUC),
		cli.WithDatabasePathSetter(setPath),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs(append(append([]string{}, args...), "--db-path", dbPath))
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}
}

func showEvent(t *testing.T, dbPath string, eventUC usecase.EventUsecase, storeUC usecase.StoreManagementUsecase, setPath func(string), eventID string, asJSON bool) string {
	t.Helper()
	stdout := &bytes.Buffer{}
	args := []string{"show", eventID, "--db-path", dbPath}
	if asJSON {
		args = append(args, "--json")
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithEvent(eventUC),
		cli.WithDatabasePathSetter(setPath),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show Execute() error = %v", err)
	}
	return stdout.String()
}

func latestAuditEventID(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var id string
	if err := db.QueryRow(`SELECT event_id FROM command_audits ORDER BY rowid DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("latest audit query error = %v", err)
	}
	return id
}

func TestRootCLI_HookAuditCommand_ClaudeReadStoresMetadataOnly(t *testing.T) {
	dbPath, eventUC, storeUC, setPath := newHookAuditStore(t)
	payload := `{"session_id":"s-read","cwd":"/tmp","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_response":{"content":"hello from read"}}`
	executeHookAudit(t, dbPath, eventUC, storeUC, setPath, []string{"hook", "audit", "claude"}, payload)

	eventID := latestAuditEventID(t, dbPath)
	shown := showEvent(t, dbPath, eventUC, storeUC, setPath, eventID, false)
	if strings.Contains(shown, "hello from read") {
		t.Fatalf("show leaked read-only output: %s", shown)
	}
	if !strings.Contains(shown, "(metadata only: read-only tool)") {
		t.Fatalf("show missing metadata label: %s", shown)
	}
	if !strings.Contains(shown, "paths: README.md") {
		t.Fatalf("show missing path: %s", shown)
	}

	jsonOut := showEvent(t, dbPath, eventUC, storeUC, setPath, eventID, true)
	if strings.Contains(jsonOut, "hello from read") {
		t.Fatalf("show JSON leaked read-only output: %s", jsonOut)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, jsonOut)
	}
	audit, _ := decoded["command_audit"].(map[string]any)
	if audit == nil {
		t.Fatalf("command_audit missing: %s", jsonOut)
	}
	if diff := cmp.Diff("", audit["output"]); diff != "" {
		t.Fatalf("JSON output mismatch (-want +got):\n%s", diff)
	}
	meta, _ := audit["output_metadata"].(map[string]any)
	if meta == nil {
		t.Fatalf("output_metadata missing: %s", jsonOut)
	}
	if diff := cmp.Diff("metadata_only", meta["capture"]); diff != "" {
		t.Fatalf("capture mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_HookAuditCommand_GrokReadFileStoresMetadataOnly(t *testing.T) {
	dbPath, eventUC, storeUC, setPath := newHookAuditStore(t)
	payload, err := os.ReadFile(filepath.Join("testdata", "grok_hooks", "v0.2.99", "post_tool_use.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	executeHookAudit(t, dbPath, eventUC, storeUC, setPath, []string{"hook", "grok", "post-tool-use"}, string(payload))

	eventID := latestAuditEventID(t, dbPath)
	shown := showEvent(t, dbPath, eventUC, storeUC, setPath, eventID, false)
	if strings.Contains(shown, "0.0.0-contract-probe") {
		t.Fatalf("show leaked grok file contents: %s", shown)
	}
	if !strings.Contains(shown, "paths: VERSION") {
		t.Fatalf("show missing grok target_file path: %s", shown)
	}
}

func TestRootCLI_HookAuditCommand_ClaudeBashKeepsFullOutput(t *testing.T) {
	dbPath, eventUC, storeUC, setPath := newHookAuditStore(t)
	payload := `{"session_id":"s-bash","cwd":"/tmp","tool_name":"Bash","tool_input":{"command":"echo hi"},"tool_response":{"stdout":"hi\n"}}`
	executeHookAudit(t, dbPath, eventUC, storeUC, setPath, []string{"hook", "audit", "claude"}, payload)

	eventID := latestAuditEventID(t, dbPath)
	shown := showEvent(t, dbPath, eventUC, storeUC, setPath, eventID, false)
	if !strings.Contains(shown, "hi") {
		t.Fatalf("show missing bash output: %s", shown)
	}
	if strings.Contains(shown, "(metadata only: read-only tool)") {
		t.Fatalf("mutating tool used metadata capture: %s", shown)
	}
}

func TestRootCLI_HookAuditCommand_DeniedReadKeepsFullOutput(t *testing.T) {
	dbPath, eventUC, storeUC, setPath := newHookAuditStore(t)
	payload, err := os.ReadFile(filepath.Join("testdata", "grok_hooks", "v0.2.99", "post_tool_use_denied.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	executeHookAudit(t, dbPath, eventUC, storeUC, setPath, []string{"hook", "grok", "post-tool-use"}, string(payload))

	eventID := latestAuditEventID(t, dbPath)
	shown := showEvent(t, dbPath, eventUC, storeUC, setPath, eventID, false)
	if !strings.Contains(shown, "Permission denied") {
		t.Fatalf("denied read dropped output: %s", shown)
	}
	if strings.Contains(shown, "(metadata only: read-only tool)") {
		t.Fatalf("denied read used metadata capture: %s", shown)
	}
}

func TestRootCLI_HookAuditCommand_KimiPassesHostAndToolName(t *testing.T) {
	eventStub := &eventUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(`{"session_id":"s-kimi","cwd":"/tmp","tool_name":"Read","tool_input":{"path":"README.md"},"tool_response":{"content":"hello"}}`))
	rootCmd.SetArgs([]string{"hook", "audit", "kimi"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if diff := cmp.Diff(types.Client("kimi"), eventStub.auditCall.host); diff != "" {
		t.Fatalf("host mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("Read", eventStub.auditCall.toolName); diff != "" {
		t.Fatalf("toolName mismatch (-want +got):\n%s", diff)
	}
}
