package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation/cli"
)

type orphanConsolidationStub struct {
	result apptypes.OrphanConsolidationResult
	err    error
	calls  []usecase.OrphanConsolidationInput
}

func (s *orphanConsolidationStub) Consolidate(_ context.Context, input usecase.OrphanConsolidationInput) (apptypes.OrphanConsolidationResult, error) {
	s.calls = append(s.calls, input)
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
			result: apptypes.OrphanConsolidationResultOf(1, true),
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
		want := "Orphan refinement candidates: 1\nCandidates: 3\n"
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

	t.Run("displays deletion count", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, false),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(4, false),
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
		want := "Orphan refinements: 4\nDeleted: 2\n"
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

	t.Run("without orphan consolidation still reports zero orphans", func(t *testing.T) {
		// Read paths and partial CLI wiring must not write refinements; when
		// the port is absent, gc still succeeds and reports 0 orphans.
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(0, time.Time{}, false),
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.WithStoreManagement(storeMaint)).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"store", "gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "Orphan refinements: 0\nDeleted: 0\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	})
}
