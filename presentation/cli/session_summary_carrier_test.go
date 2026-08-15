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

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_SessionListReadsRefinementNotSessionsSummary drives the shipped
// cobra tree against a scratch store. leftover sessions.summary is not shown;
// session end --summary writes a refinement that list surfaces.
func TestRootCLI_SessionListReadsRefinementNotSessionsSummary(t *testing.T) {
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

	listBefore := &bytes.Buffer{}
	listCmd := newRoot().Command()
	listCmd.SetOut(listBefore)
	listCmd.SetErr(&bytes.Buffer{})
	listCmd.SetArgs([]string{"session", "list", "--db-path", dbPath, "--json"})
	if err := listCmd.Execute(); err != nil {
		t.Fatalf("session list before end error = %v", err)
	}
	if strings.Contains(listBefore.String(), "leftover column text") {
		t.Fatalf("session list surfaced leftover sessions.summary:\n%s", listBefore.String())
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

	listAfter := &bytes.Buffer{}
	listAfterCmd := newRoot().Command()
	listAfterCmd.SetOut(listAfter)
	listAfterCmd.SetErr(&bytes.Buffer{})
	listAfterCmd.SetArgs([]string{"session", "list", "--db-path", dbPath, "--json"})
	if err := listAfterCmd.Execute(); err != nil {
		t.Fatalf("session list after end error = %v", err)
	}
	got := listAfter.String()
	if !strings.Contains(got, "end refinement text") {
		t.Fatalf("session list missing refinement:\n%s", got)
	}
	if strings.Contains(got, "leftover column text") {
		t.Fatalf("session list surfaced leftover sessions.summary after end:\n%s", got)
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
