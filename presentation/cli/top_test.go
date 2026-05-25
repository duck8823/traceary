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
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

// topPaneEventStub returns separate fixtures for the failures call and
// the recent-commands call so the top snapshot's two EventUsecase.List
// invocations can be distinguished by FailuresOnly / Kind criteria.
type topPaneEventStub struct {
	usecase.EventUsecase

	failures []*model.Event
	commands []*model.Event
}

func (s *topPaneEventStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	if criteria.FailuresOnly() {
		return s.failures, nil
	}
	if criteria.Kind() == types.EventKindCommandExecuted {
		return s.commands, nil
	}
	return nil, nil
}

func TestRootCLI_TopCommand_SnapshotJSONGolden(t *testing.T) {
	startedAt := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(30 * time.Minute)
	cli.SetTopNowFunc(func() time.Time { return startedAt.Add(6 * time.Hour) })
	t.Cleanup(cli.ResetTopNowFunc)

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

	failureEvent := model.EventOf(
		types.EventID("evt-fail"),
		types.EventKindCommandExecuted,
		types.Client("claude"),
		types.Agent("claude/explore"),
		types.SessionID("top-child"),
		types.Workspace("duck8823/traceary"),
		"go test ./... [exit=1]",
		startedAt.Add(20*time.Minute),
	)
	commandEvent := model.EventOf(
		types.EventID("evt-cmd"),
		types.EventKindCommandExecuted,
		types.Client("claude"),
		types.Agent("claude/explore"),
		types.SessionID("top-child"),
		types.Workspace("duck8823/traceary"),
		"ls -la",
		startedAt.Add(22*time.Minute),
	)
	eventStub := &topPaneEventStub{
		failures: []*model.Event{failureEvent},
		commands: []*model.Event{commandEvent},
	}

	candidate, err := apptypes.MemorySummaryOf(
		types.MemoryID("mem-1"),
		types.MemoryTypePreference,
		types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
		"prefer table-driven subtests",
		types.MemoryStatusCandidate,
		types.ConfidenceMedium,
		types.MemorySourceRememberIntent,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		startedAt,
		types.None[time.Time](),
		startedAt,
		startedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	staleMemory, err := apptypes.MemorySummaryOf(
		types.MemoryID("mem-stale-1"),
		types.MemoryTypeDecision,
		types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
		"superseded rollout note",
		types.MemoryStatusSuperseded,
		types.ConfidenceHigh,
		types.MemorySourceManual,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		startedAt,
		types.None[time.Time](),
		startedAt,
		startedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf(stale): %v", err)
	}
	staleRow, err := apptypes.StaleMemoryRowOf(staleMemory, apptypes.StaleMemoryReasonSuperseded)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf: %v", err)
	}
	staleResult, err := apptypes.StaleMemoryListResultOf(3, []apptypes.StaleMemoryRow{staleRow})
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf: %v", err)
	}
	memoryStub := &memoryUsecaseStub{
		listResult:  []apptypes.MemorySummary{candidate},
		staleResult: staleResult,
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(stub),
		cli.WithEvent(eventStub),
		cli.WithMemory(memoryStub),
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
		"--stale-after", "100000h",
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
	if got, want := memoryStub.staleCriteria.Limit(), 25; got != want {
		t.Fatalf("stale memory limit criteria = %d, want %d", got, want)
	}
	if got, want := memoryStub.staleCalls, 1; got != want {
		t.Fatalf("ListStale calls = %d, want %d for JSON snapshot", got, want)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "top", "snapshot_json.golden.json"))
}

func TestRootCLI_TopCommand_SnapshotTextGolden(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	startedAt := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	cli.SetTopNowFunc(func() time.Time { return startedAt.Add(6 * time.Hour) })
	t.Cleanup(cli.ResetTopNowFunc)

	stub := &sessionUsecaseStub{listResult: []apptypes.SessionSummary{
		apptypes.SessionSummaryOf(
			types.SessionID("ws-long"),
			types.Workspace("/Users/sample/repos/very/long/path/github.com/duck8823/traceary"),
			startedAt,
			types.None[time.Time](),
			"active",
			165,
			12,
			[]string{"codex"},
			"refactor session handoff",
			"",
			types.SessionID(""),
			types.Client("claude"),
			startedAt.Add(15*time.Minute),
			apptypes.SessionSummaryLatestEventOf(types.EventKindCommandExecuted, "go test ./..."),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("ws-empty"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(2*time.Hour),
			types.None[time.Time](),
			"active",
			0,
			0,
			[]string{},
			"",
			"",
			types.SessionID(""),
			types.Client("claude"),
			startedAt.Add(2*time.Hour),
		),
		// Ended root retained because an active child still references it; the
		// row exercises the `last=session_ended: …` formatting that would
		// otherwise be filtered out of `top` along with idle ended roots.
		apptypes.SessionSummaryOf(
			types.SessionID("ws-parent-ended"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(3*time.Hour),
			types.Some(startedAt.Add(3*time.Hour+30*time.Minute)),
			"ended",
			42,
			6,
			[]string{"claude"},
			"investigate failing tests",
			"",
			types.SessionID(""),
			types.Client("claude"),
			startedAt.Add(3*time.Hour+30*time.Minute),
			apptypes.SessionSummaryLatestEventOf(types.EventKindSessionEnded, "duration=29m21s"),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("ws-child-active"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(3*time.Hour+10*time.Minute),
			types.None[time.Time](),
			"active",
			7,
			3,
			[]string{"claude/explore"},
			"explore",
			"",
			types.SessionID("ws-parent-ended"),
			types.Client("claude"),
			types.EventID("spawn-ws-child-active"),
			"task",
			types.Some(1),
			startedAt.Add(3*time.Hour+25*time.Minute),
			apptypes.SessionSummaryLatestEventOf(types.EventKindTranscript, "investigating panic in usecase layer"),
		),
	}}

	failureEvent := model.EventOf(
		types.EventID("evt-fail"),
		types.EventKindCommandExecuted,
		types.Client("claude"),
		types.Agent("codex"),
		types.SessionID("ws-long"),
		types.Workspace("duck8823/traceary"),
		"go test ./... [exit=1]",
		startedAt.Add(15*time.Minute),
	)
	commandEvent := model.EventOf(
		types.EventID("evt-cmd"),
		types.EventKindCommandExecuted,
		types.Client("claude"),
		types.Agent("codex"),
		types.SessionID("ws-long"),
		types.Workspace("duck8823/traceary"),
		"go build ./...",
		startedAt.Add(20*time.Minute),
	)
	eventStub := &topPaneEventStub{
		failures: []*model.Event{failureEvent},
		commands: []*model.Event{commandEvent},
	}

	candidate, err := apptypes.MemorySummaryOf(
		types.MemoryID("mem-1"),
		types.MemoryTypePreference,
		types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
		"prefer table-driven subtests",
		types.MemoryStatusCandidate,
		types.ConfidenceMedium,
		types.MemorySourceRememberIntent,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		startedAt,
		types.None[time.Time](),
		startedAt,
		startedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	staleMemory, err := apptypes.MemorySummaryOf(
		types.MemoryID("mem-stale-1"),
		types.MemoryTypeDecision,
		types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
		"superseded rollout note",
		types.MemoryStatusSuperseded,
		types.ConfidenceHigh,
		types.MemorySourceManual,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		startedAt,
		types.None[time.Time](),
		startedAt,
		startedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf(stale): %v", err)
	}
	staleRow, err := apptypes.StaleMemoryRowOf(staleMemory, apptypes.StaleMemoryReasonSuperseded)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf: %v", err)
	}
	staleResult, err := apptypes.StaleMemoryListResultOf(3, []apptypes.StaleMemoryRow{staleRow})
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf: %v", err)
	}
	memoryStub := &memoryUsecaseStub{
		listResult:  []apptypes.MemorySummary{candidate},
		staleResult: staleResult,
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(stub),
		cli.WithEvent(eventStub),
		cli.WithMemory(memoryStub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"top",
		"--db-path", "/tmp/test-traceary.db",
		"--snapshot",
		"--stale-after", "100000h",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := memoryStub.staleCriteria.Limit(), 25; got != want {
		t.Fatalf("stale memory limit criteria = %d, want %d", got, want)
	}
	if got, want := memoryStub.staleCalls, 1; got != want {
		t.Fatalf("ListStale calls = %d, want %d for text snapshot", got, want)
	}

	assertGolden(t, stdout.Bytes(), filepath.Join("testdata", "top", "snapshot_text.golden"))
}

// TestRootCLI_TopCommand_SnapshotEmptyTextGolden covers the empty-state
// path where no active sessions, failures, recent commands, candidate
// memories, or stale memories are available. Each new section keeps its
// header and prints a localized empty-state line so script consumers get
// a stable shape.
func TestRootCLI_TopCommand_SnapshotEmptyTextGolden(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithEvent(&topPaneEventStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"top",
		"--db-path", "/tmp/test-traceary.db",
		"--snapshot",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertGolden(t, stdout.Bytes(), filepath.Join("testdata", "top", "snapshot_text_empty.golden"))
}

// TestRootCLI_TopCommand_SnapshotEmptyJSONGolden pins the JSON envelope
// shape for the empty-state case so consumers can rely on the
// `failures`, `recent_commands`, and `candidates` keys always being
// present (as empty slices / count=0) rather than omitted.
func TestRootCLI_TopCommand_SnapshotEmptyJSONGolden(t *testing.T) {
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithEvent(&topPaneEventStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"top",
		"--db-path", "/tmp/test-traceary.db",
		"--snapshot",
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "top", "snapshot_empty_json.golden.json"))
}

func TestRootCLI_SessionsCommand_SnapshotEmptyTextGolden(t *testing.T) {
	prevLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prevLocal })

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithEvent(&topPaneEventStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"sessions",
		"--db-path", "/tmp/test-traceary.db",
		"--snapshot",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertGolden(t, stdout.Bytes(), filepath.Join("testdata", "top", "snapshot_text_empty.golden"))
}

func TestRootCLI_SessionsCommand_SnapshotEmptyJSONGolden(t *testing.T) {
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
		cli.WithEvent(&topPaneEventStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"sessions",
		"--db-path", "/tmp/test-traceary.db",
		"--snapshot",
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "top", "snapshot_empty_json.golden.json"))
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
