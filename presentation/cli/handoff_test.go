package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_HandoffCommand(t *testing.T) {
	t.Parallel()

	t.Run("prints structured handoff output", func(t *testing.T) {
		t.Parallel()

		memorySummary, err := apptypes.MemorySummaryOf(
			types.MemoryID("memory-1"),
			types.MemoryTypeDecision,
			types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
			"Keep context assembly centralized",
			types.MemoryStatusAccepted,
			types.ConfidenceVerified,
			types.MemorySourceManual,
			types.None[types.MemoryID](),
			types.None[time.Time](),
			time.Now(),
			types.None[time.Time](),
			time.Now(),
			time.Now(),
		)
		if err != nil {
			t.Fatalf("MemorySummaryOf() error = %v", err)
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{
				handoff: types.Some(apptypes.ContextPackOf(
					types.SessionID("session-1"),
					types.Workspace("duck8823/traceary"),
					"v0.5.0",
					"active",
					20,
					4,
					[]string{"claude", "codex"},
					apptypes.WorkingStateOf("Finalize context semantics", "Wire CLI handoff to ContextUsecase"),
					[]string{"go test ./...", "go tool golangci-lint run"},
					[]apptypes.MemorySummary{memorySummary},
				)),
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()
		for _, needle := range []string{
			"TRACEARY HANDOFF",
			"SESSION_ID: session-1",
			"WORKSPACE: duck8823/traceary",
			"WORKING_STATE:",
			"Finalize context semantics",
			"Wire CLI handoff to ContextUsecase",
			"RECENT_COMMANDS:",
			"go test ./...",
			"MEMORIES:",
			"Keep context assembly centralized",
		} {
			if !strings.Contains(output, needle) {
				t.Fatalf("output missing %q:\n%s", needle, output)
			}
		}
	})

	t.Run("candidate memories surface in needs-review section when included", func(t *testing.T) {
		t.Parallel()

		acceptedSummary, err := apptypes.MemorySummaryOf(
			types.MemoryID("memory-accepted"),
			types.MemoryTypeDecision,
			types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
			"Keep accepted entries unchanged",
			types.MemoryStatusAccepted,
			types.ConfidenceVerified,
			types.MemorySourceManual,
			types.None[types.MemoryID](),
			types.None[time.Time](),
			time.Now(),
			types.None[time.Time](),
			time.Now(),
			time.Now(),
		)
		if err != nil {
			t.Fatalf("MemorySummaryOf(accepted) error = %v", err)
		}
		candidateSummary, err := apptypes.MemorySummaryOf(
			types.MemoryID("memory-candidate"),
			types.MemoryTypeLesson,
			types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
			"Pending review item from extraction",
			types.MemoryStatusCandidate,
			types.ConfidenceLow,
			types.MemorySourceExtracted,
			types.None[types.MemoryID](),
			types.None[time.Time](),
			time.Now(),
			types.None[time.Time](),
			time.Now(),
			time.Now(),
		)
		if err != nil {
			t.Fatalf("MemorySummaryOf(candidate) error = %v", err)
		}

		pack := apptypes.ContextPackOf(
			types.SessionID("session-marker"),
			types.Workspace("duck8823/traceary"),
			"",
			"active",
			0,
			0,
			nil,
			apptypes.WorkingStateOf("", ""),
			nil,
			[]apptypes.MemorySummary{acceptedSummary},
		).
			WithMemoryNeedsReview([]apptypes.MemorySummary{candidateSummary}, 1).
			WithMemoryCounts(1, 1)

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{
				handoff: types.Some(pack),
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "MEMORY_NEEDS_REVIEW:") {
			t.Fatalf("candidate memory should render in needs-review section:\n%s", out)
		}
		if !strings.Contains(out, "[lesson][workspace:duck8823/traceary] Pending review item from extraction") {
			t.Fatalf("candidate memory should render without being mixed into trusted memories:\n%s", out)
		}
		if strings.Contains(out, "[accepted][decision]") {
			t.Fatalf("accepted memory must not get a status prefix (existing layout); output:\n%s", out)
		}
	})

	t.Run("default propagates the 24h stale threshold to the criteria", func(t *testing.T) {
		t.Parallel()

		ctxStub := &contextUsecaseStub{
			handoff: types.Some(apptypes.ContextPackOf(
				types.SessionID("session-fresh"),
				types.Workspace("duck8823/traceary"),
				"",
				"active",
				1,
				0,
				nil,
				apptypes.WorkingStateOf("", ""),
				nil,
				nil,
			)),
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(ctxStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(ctxStub.handoffCalls) == 0 {
			t.Fatalf("expected at least one Handoff call")
		}
		first := ctxStub.handoffCalls[0]
		if first.AllowStale() {
			t.Fatalf("default AllowStale = true, want false")
		}
		if got, want := first.StaleAfter(), 24*time.Hour; got != want {
			t.Fatalf("default StaleAfter = %s, want %s", got, want)
		}
	})

	t.Run("--include-candidates opts into review candidate section", func(t *testing.T) {
		t.Parallel()

		ctxStub := &contextUsecaseStub{
			handoff: types.Some(apptypes.ContextPackOf(
				types.SessionID("session-candidates-flag"),
				types.Workspace("duck8823/traceary"),
				"",
				"active",
				1,
				0,
				nil,
				apptypes.WorkingStateOf("", ""),
				nil,
				nil,
			)),
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(ctxStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--include-candidates", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(ctxStub.handoffCalls) != 1 {
			t.Fatalf("handoff calls = %d, want 1", len(ctxStub.handoffCalls))
		}
		if !ctxStub.handoffCalls[0].IncludeMemoryCandidates() {
			t.Fatalf("IncludeMemoryCandidates() = false, want true")
		}
	})

	t.Run("default skips a stale active session and surfaces a hint", func(t *testing.T) {
		t.Parallel()

		stalePack := apptypes.ContextPackOf(
			types.SessionID("session-stale"),
			types.Workspace("duck8823/traceary"),
			"",
			"active",
			3,
			1,
			nil,
			apptypes.WorkingStateOf("", ""),
			nil,
			nil,
		)
		// First call (AllowStale=false) returns None to simulate the
		// builder skipping a stale active session; the re-query
		// (AllowStale=true) returns the stale pack so the CLI can
		// surface a targeted hint.
		ctxStub := &contextUsecaseStub{
			handoffFn: func(criteria apptypes.ContextPackCriteria) (types.Optional[apptypes.ContextPack], error) {
				if criteria.AllowStale() {
					return types.Some(stalePack), nil
				}
				return types.None[apptypes.ContextPack](), nil
			},
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(ctxStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want stale-active-session error")
		}
		if !strings.Contains(err.Error(), "session-stale") || !strings.Contains(err.Error(), "--allow-stale") {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ctxStub.handoffCalls) != 2 {
			t.Fatalf("expected 2 Handoff calls (first + recheck), got %d", len(ctxStub.handoffCalls))
		}
		if ctxStub.handoffCalls[0].AllowStale() {
			t.Fatalf("first call AllowStale = true, want false")
		}
		if !ctxStub.handoffCalls[1].AllowStale() {
			t.Fatalf("recheck AllowStale = false, want true")
		}
	})

	t.Run("--allow-stale opts in and surfaces the stale session directly", func(t *testing.T) {
		t.Parallel()

		stalePack := apptypes.ContextPackOf(
			types.SessionID("session-stale"),
			types.Workspace("duck8823/traceary"),
			"",
			"active",
			0,
			0,
			nil,
			apptypes.WorkingStateOf("", ""),
			nil,
			nil,
		)
		ctxStub := &contextUsecaseStub{
			handoff: types.Some(stalePack),
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(ctxStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"session", "handoff",
			"--db-path", filepath.Join(t.TempDir(), "traceary.db"),
			"--allow-stale",
			"--stale-after", "1h",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(stdout.String(), "SESSION_ID: session-stale") {
			t.Fatalf("expected stale session to be rendered under --allow-stale:\n%s", stdout.String())
		}
		if len(ctxStub.handoffCalls) != 1 {
			t.Fatalf("expected exactly 1 Handoff call (no recheck under --allow-stale), got %d", len(ctxStub.handoffCalls))
		}
		first := ctxStub.handoffCalls[0]
		if !first.AllowStale() {
			t.Fatalf("AllowStale = false, want true after --allow-stale")
		}
		if got, want := first.StaleAfter(), time.Hour; got != want {
			t.Fatalf("StaleAfter = %s, want %s", got, want)
		}
	})

	t.Run("ended session passes through without rechecking", func(t *testing.T) {
		t.Parallel()

		ctxStub := &contextUsecaseStub{
			handoff: types.Some(apptypes.ContextPackOf(
				types.SessionID("session-ended"),
				types.Workspace("duck8823/traceary"),
				"",
				"ended",
				5,
				0,
				nil,
				apptypes.WorkingStateOf("done", ""),
				nil,
				nil,
			)),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(ctxStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(ctxStub.handoffCalls) != 1 {
			t.Fatalf("expected 1 Handoff call when session is returned, got %d", len(ctxStub.handoffCalls))
		}
	})

	t.Run("workspace fallback note surfaces when matched through parent workspace", func(t *testing.T) {
		t.Parallel()

		parent := types.Workspace("/Users/duck/repos/project")
		child := types.Workspace("/Users/duck/repos/project/sub")

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{
				handoff: types.Some(apptypes.ContextPackOf(
					types.SessionID("session-parent"),
					parent,
					"",
					"active",
					3,
					1,
					nil,
					apptypes.WorkingStateOf("", ""),
					nil,
					nil,
				).WithRequestedWorkspace(child)),
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		out := stdout.String()
		wantNote := "NOTE: matched through parent workspace " + parent.String() + " (requested " + child.String() + ")"
		if !strings.Contains(out, wantNote) {
			t.Fatalf("output missing parent-workspace fallback note %q:\n%s", wantNote, out)
		}
	})

	t.Run("session handoff subcommand reuses the same structured output", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{
				handoff: types.Some(apptypes.ContextPackOf(
					types.SessionID("session-2"),
					types.Workspace("duck8823/traceary"),
					"",
					"ended",
					5,
					1,
					nil,
					apptypes.WorkingStateOf("Done", ""),
					nil,
					nil,
				)),
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(stdout.String(), "SESSION_ID: session-2") {
			t.Fatalf("output missing session handoff payload:\n%s", stdout.String())
		}
	})
}
