package cli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestReplayHookSpoolRecord_TranscriptNeverWritesConsolidationRequest(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")

	const sessionID = "sess-spool-transcript"
	fx := newSpoolConsolidationStore(t, sessionID, "claude")
	record := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "transcript",
		Client:        "claude",
		DBPath:        fx.dbPath,
		Payload:       `{"session_id":"` + sessionID + `","cwd":"/tmp","last_assistant_message":"spooled","prompt_response":"spooled"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}
	if _, err := persistHookSpoolRecord(record); err != nil {
		t.Fatalf("persist: %v", err)
	}
	replayed, failed := fx.root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 1 || failed != 0 {
		t.Fatalf("drain = %d/%d, want 1/0", replayed, failed)
	}
	if countTable(t, fx.dbPath, "consolidation_requests") != 0 {
		t.Fatal("spool transcript replay wrote a consolidation request")
	}
	if countKind(t, fx.dbPath, "transcript") == 0 {
		t.Fatal("spool transcript replay did not persist a transcript row")
	}
}

func TestReplayHookSpoolRecord_KimiStopNeverWritesConsolidationRequest(t *testing.T) {
	stateDir := t.TempDir()
	home := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	t.Setenv("HOME", home)
	t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	const sessionID = "session_00000000-0000-4000-8000-000000000001"
	seedKimiSessionForSpool(t, home, sessionID, []string{
		`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
		`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"kimi spool consolidation probe"}}}`,
	})
	fx := newSpoolConsolidationStore(t, sessionID, "kimi")
	payload, err := os.ReadFile(filepath.Join("testdata", "kimi_hooks", "v0.27.0", "stop.json"))
	if err != nil {
		t.Fatalf("read stop fixture: %v", err)
	}
	record := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "kimi",
		Client:        "kimi",
		Action:        "stop",
		DBPath:        fx.dbPath,
		Payload:       string(payload),
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}
	if _, err := persistHookSpoolRecord(record); err != nil {
		t.Fatalf("persist: %v", err)
	}
	replayed, failed := fx.root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 1 || failed != 0 {
		t.Fatalf("drain = %d/%d, want 1/0", replayed, failed)
	}
	if countTable(t, fx.dbPath, "consolidation_requests") != 0 {
		t.Fatal("spool kimi stop replay wrote a consolidation request")
	}
	if countKind(t, fx.dbPath, "transcript") == 0 {
		t.Fatal("spool kimi stop replay did not persist a transcript row")
	}
}

type spoolConsolidationStore struct {
	dbPath string
	root   *RootCLI
}

func newSpoolConsolidationStore(t *testing.T, sessionID, client string) spoolConsolidationStore {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	refinementDS := sqliteinfra.NewSessionRefinementDatasource(db)
	ledger := sqliteinfra.NewConsolidationRequestDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	session := model.NewSession(types.SessionID(sessionID), base, "cli", types.Agent(client), "ws")
	start, err := model.NewEventWithClock(
		"evt-start", types.EventKindSessionStarted, "cli", types.Agent(client),
		types.SessionID(sessionID), "ws", "start",
		spoolFixedClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		cmd, err := model.NewEventWithClock(
			types.EventID("evt-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, "cli", types.Agent(client),
			types.SessionID(sessionID), "ws", "cmd",
			spoolFixedClock{at: base.Add(time.Duration(i+2) * time.Second)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := eventDS.Save(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}
	root := NewRootCLI(
		WithStoreManagement(storeUC),
		WithEvent(usecase.NewEventUsecase(eventDS, eventDS)),
		WithSession(usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS, usecase.SessionUsecaseDependencies{})),
		WithConsolidationPressure(usecase.NewConsolidationPressureUsecase(eventDS, refinementDS, sessionDS, ledger)),
		WithConsolidationRequest(usecase.NewConsolidationRequestUsecase(ledger, types.SystemClock{})),
		WithSessionEventOrder(eventDS),
		WithDatabasePathSetter(db.SetPath),
	)
	return spoolConsolidationStore{dbPath: dbPath, root: root}
}

func countTable(t *testing.T, dbPath, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("COUNT %s: %v", table, err)
	}
	return count
}

func countKind(t *testing.T, dbPath, kind string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ?`, kind).Scan(&count); err != nil {
		t.Fatalf("COUNT kind: %v", err)
	}
	return count
}

type spoolFixedClock struct{ at time.Time }

func (c spoolFixedClock) Now() time.Time { return c.at }

func seedKimiSessionForSpool(t *testing.T, homeDir, sessionID string, wireRows []string) {
	t.Helper()
	sessionDir := filepath.Join(homeDir, ".kimi-code", "sessions", "wd_probe_000000000000", sessionID)
	if err := os.MkdirAll(filepath.Join(sessionDir, "agents", "main"), 0o755); err != nil {
		t.Fatalf("mkdir wire log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(strings.Join(wireRows, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write wire log: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, ".kimi-code", "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	index := `{"sessionId":"` + sessionID + `","sessionDir":"` + sessionDir + `","workDir":"/workspace/kimi-contract-probe"}` + "\n"
	if err := os.WriteFile(filepath.Join(homeDir, ".kimi-code", "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatalf("write session index: %v", err)
	}
}
