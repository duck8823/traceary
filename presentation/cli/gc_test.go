package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation/cli"
)

var errOrphanConsolidationStub = errors.New("orphan consolidation failed")

type orphanConsolidationStub struct {
	result  apptypes.OrphanConsolidationResult
	err     error
	calls   []usecase.OrphanConsolidationInput
	callLog *[]string
}

func (s *orphanConsolidationStub) Consolidate(_ context.Context, input usecase.OrphanConsolidationInput) (apptypes.OrphanConsolidationResult, error) {
	s.calls = append(s.calls, input)
	if s.callLog != nil {
		*s.callLog = append(*s.callLog, "consolidate")
	}
	if s.err != nil {
		return apptypes.OrphanConsolidationResult{}, s.err
	}
	return s.result, nil
}

func TestRootCLI_GCCommand(t *testing.T) {
	fixedNow := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	cli.SetGCNowFunc(func() time.Time { return fixedNow })
	defer cli.ResetGCNowFunc()

	t.Run("displays dry-run candidate count", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(3, time.Time{}, true),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(0, 0, 0, false, true),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(storeMaint),
			cli.WithOrphanConsolidation(orphan),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30", "--dry-run"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "Candidates: 3\nOrphan refinement candidates: 0\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
		if storeMaint.initCalled {
			t.Fatal("Initialize() was called for dry-run")
		}
		if len(orphan.calls) != 1 || !orphan.calls[0].DryRun {
			t.Fatalf("orphan consolidation calls = %+v, want one dry-run", orphan.calls)
		}
	})

	t.Run("displays collected count", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, false),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(0, 0, 0, false, false),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(storeMaint),
			cli.WithOrphanConsolidation(orphan),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "Collected: 2\nOrphan refinements: 0\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
		if !storeMaint.initCalled {
			t.Fatal("Initialize() was not called for apply")
		}
		if len(orphan.calls) != 1 || orphan.calls[0].DryRun {
			t.Fatalf("orphan consolidation calls = %+v, want one non-dry-run", orphan.calls)
		}
	})

	t.Run("missing orphan consolidation refuses to delete", func(t *testing.T) {
		// Without the consolidation port coverage would never grow again while
		// gc kept reporting success, so a wiring regression fails closed rather
		// than quietly freezing the store.
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(0, time.Time{}, false),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.WithStoreManagement(storeMaint)).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("Execute() error = nil, want a refusal to delete")
		}
		if !strings.Contains(err.Error(), "orphan consolidation is not configured") {
			t.Fatalf("Execute() error = %v, want a refusal naming the missing consolidation", err)
		}
		if storeMaint.gcCalled {
			t.Fatal("CollectGarbage() was called despite the refusal")
		}
		if stdout.String() != "" {
			t.Fatalf("stdout = %q, want no result output", stdout.String())
		}
	})

	t.Run("targets consolidation does not protect still prune without it", func(t *testing.T) {
		// memories and memory_edges hold nothing consolidation preserves, so the
		// missing port must not block them.
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(3, time.Time{}, false),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.WithStoreManagement(storeMaint)).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30", "--target", "memories"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "Deleted: 3\nOrphan refinements: 0\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	// The discard must observe the coverage that existed when the run began,
	// which is only true while it runs first. The failure cases below prove it
	// indirectly — the count is already on stdout when consolidation errors —
	// but the successful path is the one that runs every day, so the order is
	// pinned directly there too.
	t.Run("the discard runs before consolidation", func(t *testing.T) {
		for _, dryRun := range []bool{false, true} {
			name := "apply"
			if dryRun {
				name = "dry-run"
			}
			t.Run(name, func(t *testing.T) {
				var calls []string
				storeMaint := &storeManagementUsecaseStub{
					gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, dryRun),
					callLog:  &calls,
				}
				orphan := &orphanConsolidationStub{
					result:  apptypes.OrphanConsolidationResultOf(1, 1, 0, false, dryRun),
					callLog: &calls,
				}
				rootCmd := cli.NewRootCLI(
					cli.WithStoreManagement(storeMaint),
					cli.WithOrphanConsolidation(orphan),
				).Command()
				rootCmd.SetOut(&bytes.Buffer{})
				rootCmd.SetErr(&bytes.Buffer{})
				args := []string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30"}
				if dryRun {
					args = append(args, "--dry-run")
				}
				rootCmd.SetArgs(args)

				if err := rootCmd.Execute(); err != nil {
					t.Fatalf("Execute() error = %v", err)
				}
				want := []string{"gc", "consolidate"}
				if diff := cmp.Diff(want, calls); diff != "" {
					t.Fatalf("call order mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})

	// Consolidation gates on the target: it must not touch a memories /
	// memory_edges prune at all. Its failure is reported, but it no longer
	// aborts the discard, which has already run and been printed by then — the
	// discard requires a covering refinement, so a consolidation that produced
	// none cannot have widened what it reaches.
	t.Run("orphan consolidation gates on target", func(t *testing.T) {
		tests := []struct {
			name       string
			target     string
			wantCalls  int
			wantErr    bool
			wantStdout string
		}{
			{name: "events reports the discard then the failure", target: "events", wantCalls: 1, wantErr: true, wantStdout: "Discarded bodies: 2\n"},
			{name: "sessions reports the deletion then the failure", target: "sessions", wantCalls: 1, wantErr: true, wantStdout: "Deleted: 2\n"},
			{name: "all reports the collection then the failure", target: "all", wantCalls: 1, wantErr: true, wantStdout: "Collected: 2\n"},
			{name: "memories proceeds without consolidating", target: "memories", wantCalls: 0, wantStdout: "Deleted: 2\nOrphan refinements: 0\n"},
			{name: "memory_edges proceeds without consolidating", target: "memory_edges", wantCalls: 0, wantStdout: "Deleted: 2\nOrphan refinements: 0\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				storeMaint := &storeManagementUsecaseStub{
					gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, false),
				}
				orphan := &orphanConsolidationStub{err: errOrphanConsolidationStub}
				stdout := &bytes.Buffer{}
				rootCmd := cli.NewRootCLI(
					cli.WithStoreManagement(storeMaint),
					cli.WithOrphanConsolidation(orphan),
				).Command()
				rootCmd.SetOut(stdout)
				rootCmd.SetErr(&bytes.Buffer{})
				rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30", "--target", tt.target})

				err := rootCmd.Execute()
				if tt.wantErr && err == nil {
					t.Fatal("Execute() error = nil, want the consolidation failure to surface")
				}
				if !tt.wantErr && err != nil {
					t.Fatalf("Execute() error = %v, want nil", err)
				}
				if len(orphan.calls) != tt.wantCalls {
					t.Fatalf("orphan consolidation calls = %d, want %d", len(orphan.calls), tt.wantCalls)
				}
				if !storeMaint.gcCalled {
					t.Fatal("CollectGarbage was not called")
				}
				// The count is on stdout even in the failing cases, which is
				// what proves the discard ran before consolidation rather than
				// after it: an irreversible step is never silently unreported.
				if stdout.String() != tt.wantStdout {
					t.Fatalf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
				}
			})
		}
	})

	// Incomplete consolidation used to block the deletion, because deletion
	// was by cutoff alone. The discard now requires coverage, so an unfolded
	// range is out of reach whether or not the pass finished; the run says so
	// and still discards what earlier runs folded.
	t.Run("incomplete consolidation still discards what is already folded", func(t *testing.T) {
		tests := []struct {
			name   string
			result apptypes.OrphanConsolidationResult
			want   string
		}{
			{
				name:   "HasMore",
				result: apptypes.OrphanConsolidationResultOf(5000, 5000, 0, true, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 5000\n" +
					"More orphan ranges remain; re-run gc to continue consolidation\n",
			},
			{
				name:   "Skipped",
				result: apptypes.OrphanConsolidationResultOf(10, 8, 2, false, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 8\n" +
					"Orphan ranges skipped: 2\n" +
					"More orphan ranges remain; re-run gc to continue consolidation\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				storeMaint := &storeManagementUsecaseStub{
					gcResult: apptypes.CollectGarbageResultOf(99, time.Time{}, false),
				}
				orphan := &orphanConsolidationStub{result: tt.result}
				stdout := &bytes.Buffer{}
				rootCmd := cli.NewRootCLI(
					cli.WithStoreManagement(storeMaint),
					cli.WithOrphanConsolidation(orphan),
				).Command()
				rootCmd.SetOut(stdout)
				rootCmd.SetErr(&bytes.Buffer{})
				rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30"})

				if err := rootCmd.Execute(); err != nil {
					t.Fatalf("Execute() error = %v", err)
				}
				if stdout.String() != tt.want {
					t.Fatalf("stdout = %q, want %q", stdout.String(), tt.want)
				}
			})
		}
	})

	// The discard reads the coverage that exists when the run begins, so what
	// this run folds is what the next run discards. Folding therefore never
	// suppresses the discard, and the count does not depend on how much was
	// folded alongside it.
	t.Run("what a run folds does not change what it discards", func(t *testing.T) {
		tests := []struct {
			name     string
			produced int
		}{
			{name: "nothing new to fold", produced: 0},
			{name: "folded three ranges", produced: 3},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				storeMaint := &storeManagementUsecaseStub{
					gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, false),
				}
				orphan := &orphanConsolidationStub{
					result: apptypes.OrphanConsolidationResultOf(tt.produced, tt.produced, 0, false, false),
				}
				stdout := &bytes.Buffer{}
				rootCmd := cli.NewRootCLI(
					cli.WithStoreManagement(storeMaint),
					cli.WithOrphanConsolidation(orphan),
				).Command()
				rootCmd.SetOut(stdout)
				rootCmd.SetErr(&bytes.Buffer{})
				rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30"})

				if err := rootCmd.Execute(); err != nil {
					t.Fatalf("Execute() error = %v", err)
				}
				want := "Collected: 2\n" + fmt.Sprintf("Orphan refinements: %d\n", tt.produced)
				if stdout.String() != want {
					t.Fatalf("stdout = %q, want %q", stdout.String(), want)
				}
			})
		}
	})

	// The preview is exact because it and the apply both read the coverage
	// that existed before consolidation: the same store, not a store the apply
	// has already changed.
	t.Run("dry-run counts what an apply would discard even when it would fold", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(3, time.Time{}, true),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(2, 2, 0, false, true),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(storeMaint),
			cli.WithOrphanConsolidation(orphan),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30", "--dry-run"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "Candidates: 3\n" +
			"Orphan refinement candidates: 2\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("dry-run still reports counts when incomplete", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(7, time.Time{}, true),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(5, 4, 1, true, true),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(storeMaint),
			cli.WithOrphanConsolidation(orphan),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30", "--dry-run"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "Candidates: 7\n" +
			"Orphan refinement candidates: 4\n" +
			"Orphan ranges skipped: 1\n" +
			"More orphan ranges remain; re-run gc to continue consolidation\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
		if !storeMaint.gcCalled {
			t.Fatal("CollectGarbage must still run on dry-run even when incomplete")
		}
	})
}
