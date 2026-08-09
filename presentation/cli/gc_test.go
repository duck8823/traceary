package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
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
			result: apptypes.OrphanConsolidationResultOf(1, 1, 0, false, true),
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

	t.Run("displays collected count", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, false),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(4, 4, 0, false, false),
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
		want := "Orphan refinements: 4\nCollected: 2\n"
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
		// Deletion checks the retention cutoff and nothing else, so without the
		// consolidation port there is no way to know an event has a summary.
		// This used to fall through and delete; it now fails closed, because the
		// failure mode of a wiring regression is irreversible.
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
		want := "Orphan refinements: 0\nDeleted: 3\n"
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

	t.Run("incomplete consolidation skips cleanup", func(t *testing.T) {
		tests := []struct {
			name   string
			result apptypes.OrphanConsolidationResult
			want   string
		}{
			{
				name:   "HasMore blocks deletion",
				result: apptypes.OrphanConsolidationResultOf(5000, 5000, 0, true, false),
				want: "Orphan refinements: 5000\n" +
					"Cleanup skipped: orphan ranges are not fully consolidated; re-run gc to continue\n",
			},
			{
				name:   "Skipped blocks deletion and prints skip line",
				result: apptypes.OrphanConsolidationResultOf(10, 8, 2, false, false),
				want: "Orphan refinements: 8\n" +
					"Orphan ranges skipped: 2\n" +
					"Cleanup skipped: orphan ranges are not fully consolidated; re-run gc to continue\n",
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
				if storeMaint.gcCalled {
					t.Fatal("CollectGarbage was called; incomplete consolidation must skip deletion")
				}
			})
		}
	})

	// gc consolidates before it discards, and a dry run consolidates without
	// writing. The discard is therefore bounded by the instant the run began,
	// so it can only remove bodies whose coverage a preview would also have
	// seen. Capturing that instant after consolidation instead would defeat the
	// bound, and nothing in the printed output would show it.
	t.Run("complete consolidation collects and bounds the discard by the run start", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, false),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(3, 3, 0, false, false),
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
		want := "Orphan refinements: 3\nCollected: 2\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
		if !storeMaint.gcCalled {
			t.Fatal("CollectGarbage was not called for complete consolidation")
		}
		if !storeMaint.gcFoldedBefore.Equal(fixedNow) {
			t.Fatalf("foldedBefore = %v, want the run start %v", storeMaint.gcFoldedBefore, fixedNow)
		}
		wantCutoff := fixedNow.AddDate(0, 0, -30)
		if !storeMaint.gcCutoff.Equal(wantCutoff) {
			t.Fatalf("cutoff = %v, want %v", storeMaint.gcCutoff, wantCutoff)
		}
	})

	t.Run("complete consolidation with nothing new to fold collects", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, false),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(3, 0, 0, false, false),
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
		want := "Orphan refinements: 0\nCollected: 2\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
		if !storeMaint.gcCalled {
			t.Fatal("CollectGarbage was not called for complete consolidation")
		}
	})

	// gc consolidates before it discards, and a dry run consolidates without
	// writing. The discard is therefore bounded by the instant the run began,
	// so it can only remove bodies whose coverage a preview would also have
	// seen. Capturing that instant after consolidation instead would defeat
	// the bound entirely, and nothing else in the output would show it.
	t.Run("bounds the discard by the instant the run began", func(t *testing.T) {
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(2, time.Time{}, false),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(3, 3, 0, false, false),
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
		if !storeMaint.gcFoldedBefore.Equal(fixedNow) {
			t.Fatalf("foldedBefore = %v, want the run start %v", storeMaint.gcFoldedBefore, fixedNow)
		}
		wantCutoff := fixedNow.AddDate(0, 0, -30)
		if !storeMaint.gcCutoff.Equal(wantCutoff) {
			t.Fatalf("cutoff = %v, want %v", storeMaint.gcCutoff, wantCutoff)
		}
		want := "Orphan refinements: 3\nCollected: 2\n"
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
		want := "Orphan refinement candidates: 4\n" +
			"Orphan ranges skipped: 1\n" +
			"Candidates: 7\n" +
			"More orphan ranges remain; re-run gc to continue consolidation\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
		if !storeMaint.gcCalled {
			t.Fatal("CollectGarbage must still run on dry-run even when incomplete")
		}
	})
}
