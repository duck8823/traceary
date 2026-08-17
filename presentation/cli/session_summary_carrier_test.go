package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

// leftover sessions.summary is not shown by the remaining session List
// query (used by hook workspace canonicalization). session end --summary
// writes a refinement that List surfaces.
func TestSessionListQueryReadsRefinementNotSessionsSummary(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(database))
	eventDS := sqliteinfra.NewEventDatasource(database)
	sessionDS := sqliteinfra.NewSessionDatasource(database)
	refinementDS := sqliteinfra.NewSessionRefinementDatasource(database)
	refinementUC := usecase.NewSessionRefinementUsecase(sessionDS, refinementDS, eventDS, types.SystemClock{})
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS, usecase.SessionUsecaseDependencies{
		Refinement: refinementUC,
	})
	newRoot := func() *cli.RootCLI {
		return cli.NewRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithSession(sessionUC),
			cli.WithSessionRefinement(refinementUC),
			cli.WithDatabasePathSetter(database.SetPath),
		)
	}

	startOut := &bytes.Buffer{}
	startCmd := newRoot().Command()
	startCmd.SetOut(startOut)
	startCmd.SetErr(&bytes.Buffer{})
	startCmd.SetArgs([]string{
		"session", "start",
		"--db-path", dbPath,
		"--session-id", "sess-1706",
		"--workspace", "github.com/duck8823/traceary",
		"--client", "cli",
		"--agent", "codex",
	})
	if err := startCmd.Execute(); err != nil {
		t.Fatalf("session start error = %v", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), `UPDATE sessions SET summary = ? WHERE session_id = ?`, "leftover column text", "sess-1706"); err != nil {
		t.Fatalf("seed leftover sessions.summary: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	before, err := sessionUC.List(context.Background(), apptypes.NewSessionListCriteriaBuilder(20).Build())
	if err != nil {
		t.Fatalf("List() before end error = %v", err)
	}
	for _, summary := range before {
		if strings.Contains(summary.Summary(), "leftover column text") {
			t.Fatalf("List() surfaced leftover sessions.summary: %#v", before)
		}
	}

	endCmd := newRoot().Command()
	endCmd.SetOut(&bytes.Buffer{})
	endCmd.SetErr(&bytes.Buffer{})
	endCmd.SetArgs([]string{
		"session", "end",
		"--db-path", dbPath,
		"--session-id", "sess-1706",
		"--summary", "end refinement text",
	})
	if err := endCmd.Execute(); err != nil {
		t.Fatalf("session end --summary error = %v", err)
	}

	after, err := sessionUC.List(context.Background(), apptypes.NewSessionListCriteriaBuilder(20).Build())
	if err != nil {
		t.Fatalf("List() after end error = %v", err)
	}
	var got string
	for _, summary := range after {
		got += summary.Summary()
	}
	if !strings.Contains(got, "end refinement text") {
		t.Fatalf("List() missing refinement: %#v", after)
	}
	if strings.Contains(got, "leftover column text") {
		t.Fatalf("List() surfaced leftover sessions.summary after end: %#v", after)
	}

	conn, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() after end error = %v", err)
	}
	defer func() { _ = conn.Close() }()
	var column string
	if err := conn.QueryRowContext(context.Background(), `SELECT summary FROM sessions WHERE session_id = ?`, "sess-1706").Scan(&column); err != nil {
		t.Fatalf("read leftover column: %v", err)
	}
	if column != "leftover column text" {
		t.Fatalf("sessions.summary = %q, want leftover column unchanged", column)
	}
}
