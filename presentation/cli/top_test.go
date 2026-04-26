package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_TopCommand_SnapshotJSONGolden(t *testing.T) {
	startedAt := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(30 * time.Minute)

	stub := &sessionUsecaseStub{listResult: []apptypes.SessionSummary{
		apptypes.SessionSummaryOf(
			types.SessionID("top-root"),
			types.Workspace("duck8823/traceary"),
			startedAt,
			types.Some(endedAt),
			"ended",
			12,
			5,
			[]string{"claude"},
			"root",
			"Root session retained because a child is active.",
			types.SessionID(""),
			types.Client("claude"),
			endedAt,
			apptypes.SessionSummaryLatestEventOf(types.EventKindSessionEnded, "Root session ended."),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("top-child"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(10*time.Minute),
			types.None[time.Time](),
			"active",
			7,
			3,
			[]string{"claude/explore"},
			"child",
			"Active child session.",
			types.SessionID("top-root"),
			types.Client("claude"),
			types.EventID("spawn-top-child"),
			"task",
			types.Some(1),
			startedAt.Add(20*time.Minute),
			apptypes.SessionSummaryLatestEventOf(types.EventKindTranscript, "Working on active child."),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("top-ended-only"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(time.Hour),
			types.Some(startedAt.Add(2*time.Hour)),
			"ended",
			2,
			0,
			[]string{"codex"},
			"ended",
			"This lineage is pruned from traceary top.",
			types.SessionID(""),
			types.Client("codex"),
		),
	}}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(stub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"top",
		"--db-path", "/tmp/test-traceary.db",
		"--workspace", "duck8823/traceary",
		"--client", "claude",
		"--agent", "claude/explore",
		"--snapshot",
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := stub.listCriteria.Limit(), 500; got != want {
		t.Fatalf("limit criteria = %d, want %d", got, want)
	}

	if !stub.listCriteria.ActiveOnly() {
		t.Fatalf("ActiveOnly criteria = false, want true")
	}
	if got, want := stub.listCriteria.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("workspace criteria = %q, want %q", got, want)
	}
	if got, want := stub.listCriteria.Client().String(), "claude"; got != want {
		t.Fatalf("client criteria = %q, want %q", got, want)
	}
	if got, want := stub.listCriteria.Agent().String(), "claude/explore"; got != want {
		t.Fatalf("agent criteria = %q, want %q", got, want)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "top", "snapshot_json.golden.json"))
}

func TestRootCLI_TopCommand_JSONRequiresSnapshot(t *testing.T) {
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"top", "--db-path", "/tmp/test-traceary.db", "--json"})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want --json without --snapshot error")
	}
}

func TestRootCLI_TopCommand_AppliesActiveFiltersBeforeLimit(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)

	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := sessionUC.Start(ctx, types.Client("hook"), types.Agent("codex"), types.SessionID("active-matching-session"), types.Workspace("workspace-a"), types.SessionID("")); err != nil {
		t.Fatalf("Start(active) error = %v", err)
	}
	time.Sleep(time.Millisecond)
	for i := range 100 {
		sid := types.SessionID(fmt.Sprintf("ended-%03d", i))
		if _, err := sessionUC.Start(ctx, types.Client("hook"), types.Agent("codex"), sid, types.Workspace("workspace-a"), types.SessionID("")); err != nil {
			t.Fatalf("Start(%s) error = %v", sid, err)
		}
		if _, err := sessionUC.End(ctx, types.Client("hook"), types.Agent("codex"), sid, types.Workspace("workspace-a"), "ended"); err != nil {
			t.Fatalf("End(%s) error = %v", sid, err)
		}
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithSession(sessionUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"top",
		"--db-path", dbPath,
		"--workspace", "workspace-a",
		"--client", "hook",
		"--agent", "codex",
		"--limit", "20",
		"--snapshot",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "active-matching-session") {
		t.Fatalf("top output should contain active matching session after filtering before limit, got:\n%s", output)
	}
	if strings.Contains(output, "ended-") {
		t.Fatalf("top output should not contain ended sessions, got:\n%s", output)
	}
}
