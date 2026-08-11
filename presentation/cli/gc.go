package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

const defaultRetentionDays = 90

var gcNowFunc = time.Now

func (c *RootCLI) newStoreGCCommand() *cobra.Command {
	var (
		dbPath   string
		keepDays int
		target   string
		dryRun   bool
	)

	gcCmd := &cobra.Command{
		Use:   "gc",
		Short: Localize("Discard eligible retained bodies and clean up the store", "対象の保持本文を破棄してストアを整理する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runGC(cmd.Context(), cmd.OutOrStdout(), gcCommandInput{
				dbPath:   dbPath,
				keepDays: keepDays,
				target:   target,
				dryRun:   dryRun,
			})
		},
	}
	gcCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	gcCmd.Flags().IntVar(&keepDays, "keep-days", defaultRetentionDays, Localize("number of days to retain", "保持する日数"))
	gcCmd.Flags().StringVar(&target, "target", "all", Localize("targets to clean up; events discards eligible bodies (events | sessions | memories | memory_edges | all)", "整理対象。events は対象本文を破棄 (events | sessions | memories | memory_edges | all)"))
	gcCmd.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		Localize(
			"count candidates using a read-only connection without initializing or migrating the store",
			"storeをinitialize・migrationせず、read-only connectionで対象件数だけを表示する",
		),
	)

	return gcCmd
}

func (c *RootCLI) runGC(ctx context.Context, output io.Writer, input gcCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	if input.keepDays <= 0 {
		return xerrors.New(Localize("--keep-days must be greater than or equal to 1", "keep-days は 1 以上である必要があります"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if !input.dryRun {
		if err := c.storeManagement.Initialize(ctx); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
		}
	}

	target, ok := apptypes.GarbageCollectionTargetFrom(input.target)
	if !ok {
		return xerrors.New(Localize("--target must be one of events, sessions, memories, memory_edges, all", "--target は events, sessions, memories, memory_edges, all のいずれかである必要があります"))
	}

	// Orphan consolidation is a step inside store gc, not a command or a
	// --target value, and it only runs for targets that can remove the material
	// it summarises. A memories or memory_edges prune touches nothing
	// consolidation protects, so it must not be blocked by an unrelated
	// consolidation failure.
	//
	// A missing use case fails closed. main.go always wires it, so this is
	// unreachable in the shipped binary — but a future wiring regression would
	// leave coverage permanently frozen while gc kept reporting success, and
	// the check that rules that out costs one comparison.
	consolidates := orphanConsolidationAppliesTo(target)
	if consolidates && c.orphanConsolidation == nil {
		return xerrors.New(Localize(
			"orphan consolidation is not configured; refusing to discard event bodies that may have no summary",
			"orphan range の機械要約が設定されていません。要約のない event 本文を破棄する恐れがあるため中止します",
		))
	}

	// The discard runs before consolidation, and that ordering is the whole
	// safety argument for --dry-run.
	//
	// The discard only touches bodies a refinement already covers, so it acts
	// on the coverage that exists when the run begins. A dry run consolidates
	// without writing and therefore sees exactly that coverage too; if the
	// apply folded first, it would discard bodies the preview could not have
	// counted — the one loss --dry-run exists to make visible. Running the
	// discard first makes the two see the same store by construction, with no
	// timestamp standing in for "was this already folded".
	//
	// Consolidating afterwards is not wasted: what this run folds is what the
	// next run discards, and that run's preview counts it first. Nothing is
	// stranded, because coverage only ever grows.
	//
	// A consolidation failure no longer has to abort ahead of the discard. It
	// means no new coverage landed, and a discard that requires coverage
	// cannot act on material that has none.
	cutoff := gcNowFunc().AddDate(0, 0, -input.keepDays)
	result, err := c.storeManagement.CollectGarbage(ctx, cutoff, target, input.dryRun)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to run garbage collection", "gc の実行に失敗しました"), err)
	}
	// Printed before consolidation runs so an irreversible discard is always
	// reported, even when the consolidation that follows it fails.
	if err := printGCCount(output, input.dryRun, target, result.DeletedCount()); err != nil {
		return err
	}

	var orphanResult apptypes.OrphanConsolidationResult
	if consolidates {
		var orphanErr error
		orphanResult, orphanErr = c.orphanConsolidation.Consolidate(ctx, usecase.OrphanConsolidationInput{
			StaleAfter: defaultActiveSessionStaleAfter,
			DryRun:     input.dryRun,
		})
		if orphanErr != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to consolidate orphan ranges", "orphan range の機械要約に失敗しました"), orphanErr)
		}
	}

	return printOrphanResult(output, input.dryRun, orphanResult)
}

// maxOrphanFailureReasonRunes bounds one reported reason. The reasons are
// wrapped error chains, which are unbounded in principle and can carry a
// query or a row value; a listing whose lines can be arbitrarily long stops
// being readable exactly when there is most to read. The full text is still
// written to the log by the usecase, so nothing is lost, only shortened here.
const maxOrphanFailureReasonRunes = 200

// singleLineFailureReason renders one failure reason as exactly one line. A
// wrapped error may contain newlines, and a multi-line reason would break the
// "session: reason" shape the listing depends on — the second line would read
// as if it were another session.
func singleLineFailureReason(reason string) string {
	collapsed := strings.Join(strings.Fields(reason), " ")
	if collapsed == "" {
		return Localize("(no reason reported)", "(理由の報告なし)")
	}
	runes := []rune(collapsed)
	if len(runes) <= maxOrphanFailureReasonRunes {
		return collapsed
	}
	return string(runes[:maxOrphanFailureReasonRunes]) + "…"
}

// printOrphanResult reports the consolidation half of a run. Only the produced
// count reads differently between a preview and an apply; the rest is one
// message set, so it is written once.
func printOrphanResult(output io.Writer, dryRun bool, result apptypes.OrphanConsolidationResult) error {
	produced := Localize("Orphan refinements", "orphan 機械要約")
	if dryRun {
		produced = Localize("Orphan refinement candidates", "orphan 機械要約候補")
	}
	if _, err := fmt.Fprintf(output, "%s: %d\n", produced, result.ProducedCount()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
	}
	if result.Skipped() > 0 {
		if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Orphan ranges skipped", "orphan range のスキップ"), result.Skipped()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
		}
		if _, err := fmt.Fprintf(output, "%s\n", Localize(
			"Skipped orphan range failures (re-running gc will skip these again):",
			"スキップした orphan range の失敗（gc を再実行しても同じ範囲を再びスキップします）:",
		)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
		}
		failures := result.Failures().Items()
		for _, failure := range failures {
			if _, err := fmt.Fprintf(output, "  %s: %s\n", failure.SessionID(), singleLineFailureReason(failure.Reason())); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
			}
		}
		if result.Failures().Truncated() {
			if _, err := fmt.Fprintf(output, Localize(
				"Only the first %d skipped orphan range failures are listed.\n",
				"スキップした orphan range の失敗は先頭 %d 件だけ表示しています。\n",
			), len(failures)); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
			}
		}
	}
	// A target that does not consolidate leaves the zero result, which has no
	// more candidates, so this stays silent rather than claiming ranges remain.
	// It asks about remaining candidates and nothing else: a pass that skipped
	// everything it found has no more work for a re-run to do, and telling the
	// operator otherwise is what #1795 was.
	if result.HasMore() {
		if _, err := fmt.Fprintf(output, "%s\n", Localize(
			"More orphan ranges remain; re-run gc to continue consolidation",
			"orphan range が残っています。機械要約を続けるには gc を再実行してください",
		)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
		}
	}
	return nil
}

// printGCCount reports what the garbage collection did. The count means
// different things per target, so the label has to as well: events only
// discards bodies, all mixes discards with deletions, and the remaining
// targets still delete rows.
func printGCCount(output io.Writer, dryRun bool, target apptypes.GarbageCollectionTarget, count int) error {
	label := Localize("Candidates", "対象候補")
	if !dryRun {
		switch target {
		case apptypes.GarbageCollectionTargetEvents:
			label = Localize("Discarded bodies", "破棄した本文")
		case apptypes.GarbageCollectionTargetAll:
			label = Localize("Collected", "整理しました")
		default:
			label = Localize("Deleted", "削除しました")
		}
	}
	if _, err := fmt.Fprintf(output, "%s: %d\n", label, count); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
	}
	return nil
}

// orphanConsolidationAppliesTo reports whether a target can remove the events
// an orphan range summarises. sessions is included because pruning a session
// row leaves its refinement without the boundary that names it.
func orphanConsolidationAppliesTo(target apptypes.GarbageCollectionTarget) bool {
	switch target {
	case apptypes.GarbageCollectionTargetEvents,
		apptypes.GarbageCollectionTargetSessions,
		apptypes.GarbageCollectionTargetAll:
		return true
	default:
		return false
	}
}
