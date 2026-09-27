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
		rootCmd.SetArgs([]string{"context", "--handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

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

	t.Run("memory candidates surface in needs-review section when included", func(t *testing.T) {
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
		rootCmd.SetArgs([]string{"context", "--handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "MEMORY_NEEDS_REVIEW:") {
			t.Fatalf("memory candidate should render in needs-review section:\n%s", out)
		}
		if !strings.Contains(out, "[lesson][workspace:duck8823/traceary] Pending review item from extraction") {
			t.Fatalf("memory candidate should render without being mixed into trusted memories:\n%s", out)
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
		rootCmd.SetArgs([]string{"context", "--handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

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
		rootCmd.SetArgs([]string{"context", "--handoff", "--include-candidates", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

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

	t.Run("empty lookup does not requery lifecycle eligibility", func(t *testing.T) {
		ctxStub := &contextUsecaseStub{}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithContext(ctxStub)).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"context", "--handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if len(ctxStub.handoffCalls) != 1 {
			t.Fatalf("calls = %d, want 1", len(ctxStub.handoffCalls))
		}
		if !strings.Contains(stdout.String(), "No matching session handoff.") {
			t.Fatal(stdout.String())
		}
	})

	t.Run("--allow-stale opts in and surfaces the stale session directly", func(t *testing.T) {
		t.Parallel()

		stalePack := apptypes.ContextPackOf(
			types.SessionID("session-stale"),
			types.Workspace("duck8823/traceary"),
			"",
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
			"context", "--handoff",
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
		rootCmd.SetArgs([]string{"context", "--handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

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
		rootCmd.SetArgs([]string{"context", "--handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		out := stdout.String()
		wantNote := "NOTE: matched through parent workspace " + parent.String() + " (requested " + child.String() + ")"
		if !strings.Contains(out, wantNote) {
			t.Fatalf("output missing parent-workspace fallback note %q:\n%s", wantNote, out)
		}
	})

	t.Run("handoff-only flags require --handoff or --compact-only", func(t *testing.T) {
		t.Parallel()

		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{}),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"context", "--recent", "5", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("Execute() error = nil, want handoff-only flag error")
		}
		if !strings.Contains(err.Error(), "--handoff") || !strings.Contains(err.Error(), "--compact-only") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("raw-context filters cannot combine with --handoff", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			flag string
		}{
			{name: "limit", flag: "--limit"},
			{name: "client", flag: "--client"},
			{name: "agent", flag: "--agent"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				rootCmd := cli.NewRootCLI(
					cli.WithStoreManagement(&storeManagementUsecaseStub{}),
					cli.WithContext(&contextUsecaseStub{}),
				).Command()
				rootCmd.SetOut(&bytes.Buffer{})
				rootCmd.SetErr(&bytes.Buffer{})
				value := "3"
				if tt.flag != "--limit" {
					value = "codex"
				}
				rootCmd.SetArgs([]string{
					"context", "--handoff",
					tt.flag, value,
					"--db-path", filepath.Join(t.TempDir(), "traceary.db"),
				})

				err := rootCmd.Execute()
				if err == nil {
					t.Fatalf("Execute(%s) error = nil, want combination error", tt.flag)
				}
				if !strings.Contains(err.Error(), tt.flag) {
					t.Fatalf("error = %v, want %s mentioned", err, tt.flag)
				}
			})
		}
	})
}

func TestContextDeprecatedStaleFlags(t *testing.T) {
	for _, mode := range []string{"--handoff", "--compact-only"} {
		for _, flags := range [][]string{nil, {"--allow-stale=false"}, {"--stale-after", "0s"}, {"--allow-stale", "--stale-after", "1h"}} {
			t.Run(mode+strings.Join(flags, "_"), func(t *testing.T) {
				pack := apptypes.ContextPackOf("session-old", "workspace", "", 2, 1, nil, apptypes.WorkingStateOf("STATUS: ended", "preserved compact"), []string{"recent command"}, nil)
				ctxStub := &contextUsecaseStub{handoff: types.Some(pack)}
				stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
				cmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithContext(ctxStub)).Command()
				cmd.SetOut(stdout)
				cmd.SetErr(stderr)
				args := []string{"context", mode, "--db-path", filepath.Join(t.TempDir(), "traceary.db")}
				cmd.SetArgs(append(args, flags...))
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, flag := range flags {
					if strings.HasPrefix(flag, "--") {
						count++
					}
				}
				if got := strings.Count(stderr.String(), "deprecated"); got != count {
					t.Fatalf("warnings=%d want %d: %q", got, count, stderr.String())
				}
				if strings.Contains(stdout.String(), "deprecated") || strings.Contains(stdout.String(), "\nSTATUS:") {
					t.Fatal(stdout.String())
				}
				if !strings.Contains(stdout.String(), "STATUS: ended") {
					t.Fatalf("human summary lost: %q", stdout.String())
				}
				if len(ctxStub.handoffCalls) != 1 {
					t.Fatalf("calls=%d", len(ctxStub.handoffCalls))
				}
			})
		}
	}
}

func TestContextDeprecatedStaleFlagValidation(t *testing.T) {
	for _, args := range [][]string{{"--allow-stale"}, {"--stale-after", "1h"}, {"--handoff", "--stale-after", "invalid"}, {"--compact-only", "--allow-stale=invalid"}, {"--handoff", "--compact-only", "--allow-stale"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			ctxStub := &contextUsecaseStub{}
			cmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithContext(ctxStub)).Command()
			out, errs := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(errs)
			cmd.SetArgs(append([]string{"context"}, args...))
			if err := cmd.Execute(); err == nil {
				t.Fatal("invalid invocation succeeded")
			}
			if len(ctxStub.handoffCalls) != 0 {
				t.Fatal("invalid flags reached handoff")
			}
			if strings.Contains(errs.String(), "deprecated") {
				t.Fatalf("unexpected compatibility warning: %s", errs.String())
			}
		})
	}
}
