package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestCompactedAuditsStillDecodeOnListAndShow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "traceary.db")
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	database := infra.NewDatabase(dbPath, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	events := infra.NewEventDatasource(database)
	const command = "echo fixture-command"
	eventID, err := types.EventIDFrom("decode-cli")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatal(err)
	}
	ev := model.EventOf(eventID, types.EventKindCommandExecuted, "cli", agent, sessionID, "repo", "", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	if err := events.Save(ctx, ev); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed)
		VALUES (?, ?, ?, ?, 0, 0, ?, ?, 0, 0)`, "decode-cli", command, "fixture-input", "fixture-output", len("fixture-input"), len("fixture-output")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&infra.CompactionFileJournal{Dir: filepath.Join(dir, ".traceary-compaction")},
		&infra.SQLiteCompactionBuilder{},
		infra.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		infra.StoreLeaseCoordinator{},
	)
	if _, err := svc.Compact(ctx, application.CompactInput{
		Source: dbPath, KeepDays: 36500, Now: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC), Force: true,
	}); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	after := infra.NewEventDatasource(infra.NewDatabase(dbPath, migrations))
	eventUC := usecase.NewEventUsecase(after, after)
	t.Setenv("TRACEARY_DB_PATH", dbPath)
	stdout := &bytes.Buffer{}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventUC),
	).Command()
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"list", "--json", "--kind", "command_executed", "--limit", "10", "--workspace", "repo", "--db-path", dbPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("list: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), command) {
		t.Fatalf("list JSON missing command %q; got %s", command, stdout.String())
	}

	showOut := &bytes.Buffer{}
	show := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventUC),
	).Command()
	show.SetOut(showOut)
	show.SetErr(&bytes.Buffer{})
	show.SetArgs([]string{"show", "decode-cli", "--db-path", dbPath})
	if err := show.Execute(); err != nil {
		t.Fatalf("show: %v\n%s", err, showOut.String())
	}
	if !strings.Contains(showOut.String(), command) {
		t.Fatalf("show missing command %q; got %s", command, showOut.String())
	}
}
