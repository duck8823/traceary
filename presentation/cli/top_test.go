package cli_test

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
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
