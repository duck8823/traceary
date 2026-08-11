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

func orphanFailures(failures ...apptypes.OrphanConsolidationFailure) apptypes.OrphanConsolidationFailures {
	return apptypes.OrphanConsolidationFailuresOf(failures)
}

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
			result: apptypes.OrphanConsolidationResultOf(0, 0, apptypes.OrphanConsolidationFailuresOf(nil), false, true),
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
			result: apptypes.OrphanConsolidationResultOf(0, 0, apptypes.OrphanConsolidationFailuresOf(nil), false, false),
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
					result:  apptypes.OrphanConsolidationResultOf(1, 1, apptypes.OrphanConsolidationFailuresOf(nil), false, dryRun),
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
				result: apptypes.OrphanConsolidationResultOf(5000, 5000, apptypes.OrphanConsolidationFailuresOf(nil), true, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 5000\n" +
					"More orphan ranges remain; re-run gc to continue consolidation\n",
			},
			{
				name: "Skipped",
				result: apptypes.OrphanConsolidationResultOf(10, 8, orphanFailures(
					apptypes.OrphanConsolidationFailureOf("sess-bad-1", "invalid created_at"),
					apptypes.OrphanConsolidationFailureOf("sess-bad-2", "missing event timestamp"),
				), false, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 8\n" +
					"Orphan ranges skipped: 2\n" +
					"Each skipped orphan range, with the reason it failed:\n" +
					"  sess-bad-1: invalid created_at\n" +
					"  sess-bad-2: missing event timestamp\n" +
					"Re-running gc retries these; a range whose data cannot be read fails the same way each time.\n",
			},
			{
				// Both facts are true at once, and neither is allowed to
				// swallow the other: two ranges will never consolidate, and
				// there is still real work a re-run would do. The failure
				// listing says the first, the trailing line says the second,
				// and the operator can act on both.
				name: "Skipped and HasMore",
				result: apptypes.OrphanConsolidationResultOf(10, 7, orphanFailures(
					apptypes.OrphanConsolidationFailureOf("sess-bad-1", "invalid created_at"),
				), true, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 7\n" +
					"Orphan ranges skipped: 1\n" +
					"Each skipped orphan range, with the reason it failed:\n" +
					"  sess-bad-1: invalid created_at\n" +
					"Re-running gc retries these; a range whose data cannot be read fails the same way each time.\n" +
					"More orphan ranges remain; re-run gc to continue consolidation\n",
			},
			{
				// A wrapped error chain carries newlines. Printed verbatim the
				// continuation lines would read as further sessions, so the
				// reason is collapsed to one line.
				name: "multi-line reason stays on one line",
				result: apptypes.OrphanConsolidationResultOf(1, 0, orphanFailures(
					apptypes.OrphanConsolidationFailureOf("sess-bad-1", "load orphan range:\n    parse created_at:\n        empty value"),
				), false, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 0\n" +
					"Orphan ranges skipped: 1\n" +
					"Each skipped orphan range, with the reason it failed:\n" +
					"  sess-bad-1: load orphan range: parse created_at: empty value\n" +
					"Re-running gc retries these; a range whose data cannot be read fails the same way each time.\n",
			},
			{
				// An error that embeds a row value or a query is unbounded.
				// The listing stays readable; the full text is in the log.
				name: "over-long reason is elided",
				result: apptypes.OrphanConsolidationResultOf(1, 0, orphanFailures(
					apptypes.OrphanConsolidationFailureOf("sess-bad-1", strings.Repeat("x", 260)),
				), false, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 0\n" +
					"Orphan ranges skipped: 1\n" +
					"Each skipped orphan range, with the reason it failed:\n" +
					"  sess-bad-1: " + strings.Repeat("x", 200) + "\u2026\n" +
					"Re-running gc retries these; a range whose data cannot be read fails the same way each time.\n",
			},
			{
				// Both fields are read back out of the store, so both carry
				// whatever a hook wrote \u2014 the reason quotes row values, and a
				// session id is only trimmed on the way in. An ESC sequence
				// reaching the terminal could erase the lines around it,
				// including the ones this listing exists to show, so it is
				// removed before either is printed.
				name: "terminal control sequences are removed from both fields",
				result: apptypes.OrphanConsolidationResultOf(1, 0, orphanFailures(
					apptypes.OrphanConsolidationFailureOf(
						"sess-\x1b[2Jbad",
						"invalid created_at \x1b[1;31m\"\"\x1b[0m",
					),
				), false, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 0\n" +
					"Orphan ranges skipped: 1\n" +
					"Each skipped orphan range, with the reason it failed:\n" +
					"  sess-[2Jbad: invalid created_at [1;31m\"\"[0m\n" +
					"Re-running gc retries these; a range whose data cannot be read fails the same way each time.\n",
			},
			{
				// Elision cuts at a rune count, which can fall between an
				// escape and its reset. Stripping first means there is no
				// sequence left to cut in half.
				name: "an escape cannot survive elision",
				result: apptypes.OrphanConsolidationResultOf(1, 0, orphanFailures(
					apptypes.OrphanConsolidationFailureOf(
						"sess-bad-1", strings.Repeat("y", 195)+"\x1b[8m"+strings.Repeat("z", 60)+"\x1b[0m",
					),
				), false, false),
				want: "Collected: 99\n" +
					"Orphan refinements: 0\n" +
					"Orphan ranges skipped: 1\n" +
					"Each skipped orphan range, with the reason it failed:\n" +
					"  sess-bad-1: " + strings.Repeat("y", 195) + "[8m" + strings.Repeat("z", 2) + "\u2026\n" +
					"Re-running gc retries these; a range whose data cannot be read fails the same way each time.\n",
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

	t.Run("caps and declares skipped failure details", func(t *testing.T) {
		// The cap is written here as a literal rather than read from the type.
		// It is a promise to the operator about how much output one run can
		// produce, so the test has to fail when the promise changes; sharing a
		// constant with the implementation would make it agree with itself.
		const listed = 10
		failures := make([]apptypes.OrphanConsolidationFailure, 0, listed+2)
		for i := 0; i < listed+2; i++ {
			failures = append(failures, apptypes.OrphanConsolidationFailureOf(
				fmt.Sprintf("sess-bad-%02d", i), "invalid created_at",
			))
		}
		storeMaint := &storeManagementUsecaseStub{
			gcResult: apptypes.CollectGarbageResultOf(0, time.Time{}, false),
		}
		orphan := &orphanConsolidationStub{
			result: apptypes.OrphanConsolidationResultOf(12, 0, orphanFailures(failures...), false, false),
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
		got := stdout.String()
		if strings.Count(got, "  sess-bad-") != listed {
			t.Fatalf("listed failure count = %d, want %d; stdout = %q", strings.Count(got, "  sess-bad-"), listed, got)
		}
		if !strings.Contains(got, "Only the first 10 skipped orphan ranges are listed.") {
			t.Fatalf("stdout missing truncation notice: %q", got)
		}
		if strings.Contains(got, "sess-bad-10") {
			t.Fatalf("stdout listed a failure beyond the cap: %q", got)
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
					result: apptypes.OrphanConsolidationResultOf(tt.produced, tt.produced, apptypes.OrphanConsolidationFailuresOf(nil), false, false),
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
			result: apptypes.OrphanConsolidationResultOf(2, 2, apptypes.OrphanConsolidationFailuresOf(nil), false, true),
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
			result: apptypes.OrphanConsolidationResultOf(5, 4, orphanFailures(
				apptypes.OrphanConsolidationFailureOf("sess-bad", "invalid created_at"),
			), true, true),
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
			"Each skipped orphan range, with the reason it failed:\n" +
			"  sess-bad: invalid created_at\n" +
			"Re-running gc retries these; a range whose data cannot be read fails the same way each time.\n" +
			"More orphan ranges remain; re-run gc to continue consolidation\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
		if !storeMaint.gcCalled {
			t.Fatal("CollectGarbage must still run on dry-run even when incomplete")
		}
	})
}
