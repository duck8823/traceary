package cli_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation/cli"
)

var errOrphanConsolidationStub = errors.New("orphan consolidation failed")

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

	// Consolidation exists to preserve material before events disappear, so it
	// gates on the target: a failure must abort a run that would delete events
	// or sessions, and must not touch a memories / memory_edges prune at all.
	t.Run("orphan consolidation gates on target", func(t *testing.T) {
		tests := []struct {
			name        string
			target      string
			wantCalls   int
			wantErr     bool
			wantDeleted bool
		}{
			{name: "events aborts on consolidation failure", target: "events", wantCalls: 1, wantErr: true},
			{name: "sessions aborts on consolidation failure", target: "sessions", wantCalls: 1, wantErr: true},
			{name: "all aborts on consolidation failure", target: "all", wantCalls: 1, wantErr: true},
			{name: "memories proceeds without consolidating", target: "memories", wantCalls: 0, wantDeleted: true},
			{name: "memory_edges proceeds without consolidating", target: "memory_edges", wantCalls: 0, wantDeleted: true},
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
					t.Fatal("Execute() error = nil, want consolidation failure to abort")
				}
				if !tt.wantErr && err != nil {
					t.Fatalf("Execute() error = %v, want nil", err)
				}
				if len(orphan.calls) != tt.wantCalls {
					t.Fatalf("orphan consolidation calls = %d, want %d", len(orphan.calls), tt.wantCalls)
				}
				if storeMaint.gcCalled != tt.wantDeleted {
					t.Fatalf("CollectGarbage called = %t, want %t", storeMaint.gcCalled, tt.wantDeleted)
				}
			})
		}
	})
}
