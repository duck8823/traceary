package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_HookSessionRedeliveryKeepsCanonicalWorkspace(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "")
	canonicalDir := t.TempDir()
	retryDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	ctx := context.Background()
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	fire := func(args []string, payload string) {
		t.Helper()
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithEvent(eventUC),
			cli.WithSession(sessionUC),
			cli.WithDatabasePathSetter(db.SetPath),
		).Command()
		rootCmd.SetIn(strings.NewReader(payload))
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs(append(args, "--db-path", dbPath))
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
	}

	fire([]string{"hook", "session", "codex", "start"}, fmt.Sprintf(`{"session_id":"session-canonical","cwd":%q}`, canonicalDir))
	fire([]string{"hook", "session", "codex", "start"}, fmt.Sprintf(`{"session_id":"session-canonical","cwd":%q}`, retryDir))
	fire([]string{"hook", "prompt", "codex"}, `{"session_id":"session-canonical","event_id":"prompt-after-retry","prompt":"continue without cwd"}`)

	sqldb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqldb.Close() }()
	var canonical, promptWorkspace string
	if err := sqldb.QueryRow(`SELECT workspace FROM sessions WHERE session_id = 'session-canonical'`).Scan(&canonical); err != nil {
		t.Fatalf("read canonical session: %v", err)
	}
	if err := sqldb.QueryRow(`SELECT workspace FROM events WHERE kind = 'prompt' AND session_id = 'session-canonical'`).Scan(&promptWorkspace); err != nil {
		t.Fatalf("read prompt workspace: %v", err)
	}
	if canonical == "" || promptWorkspace != canonical {
		t.Fatalf("canonical/prompt workspace = %q/%q, want identical non-empty canonical fallback", canonical, promptWorkspace)
	}
	var starts, supplemental int
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'session_started' AND session_id = 'session-canonical'`).Scan(&starts); err != nil {
		t.Fatalf("count session starts: %v", err)
	}
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations WHERE observation_kind = 'supplemental' AND session_id = 'session-canonical'`).Scan(&supplemental); err != nil {
		t.Fatalf("count supplemental observations: %v", err)
	}
	if starts != 1 || supplemental != 1 {
		t.Fatalf("session starts/supplemental = %d/%d, want 1/1", starts, supplemental)
	}
}
