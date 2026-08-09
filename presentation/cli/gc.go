package cli

import (
	"context"
	"fmt"
	"io"
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

	// Orphan consolidation runs before body discard so a degraded refinement can
	// land while the events it summarises still exist. No new surface: this is
	// a step inside store gc, not a command or --target value.
	//
	// It only runs for targets that can remove that material, and its failure
	// aborts the run for exactly those targets: deleting events after the
	// summary failed to land is the irreversible half. A memories or
	// memory_edges prune touches nothing consolidation protects, so it must not
	// be blocked by an unrelated consolidation failure.
	//
	// Deletion is by cutoff only and checks no coverage, so an incomplete
	// consolidation (HasMore or Skipped > 0) must stop the deletion half for
	// the targets consolidation protects.
	//
	// A missing use case fails closed rather than falling through to deletion.
	// main.go always wires it, so this is unreachable in the shipped binary —
	// but the failure mode of a future wiring regression would be irreversible
	// deletion of events nothing summarises, which is not a failure mode worth
	// leaving open to save one check.
	var orphanResult apptypes.OrphanConsolidationResult
	consolidationApplied := false
	if orphanConsolidationAppliesTo(target) {
		if c.orphanConsolidation == nil {
			return xerrors.New(Localize(
				"orphan consolidation is not configured; refusing to discard event bodies that may have no summary",
				"orphan range の機械要約が設定されていません。要約のない event 本文を破棄する恐れがあるため中止します",
			))
		}
		var orphanErr error
		orphanResult, orphanErr = c.orphanConsolidation.Consolidate(ctx, usecase.OrphanConsolidationInput{
			StaleAfter: defaultActiveSessionStaleAfter,
			DryRun:     input.dryRun,
		})
		if orphanErr != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to consolidate orphan ranges", "orphan range の機械要約に失敗しました"), orphanErr)
		}
		consolidationApplied = true
	}

	// Dry-run still runs CollectGarbage for its count because a dry run deletes
	// nothing. Apply mode must not delete when consolidation left uncovered ranges.
	skipDeletion := consolidationApplied && !orphanResult.Complete() && !input.dryRun
	var result apptypes.CollectGarbageResult
	if !skipDeletion {
		cutoff := gcNowFunc().AddDate(0, 0, -input.keepDays)
		var gcErr error
		result, gcErr = c.storeManagement.CollectGarbage(ctx, cutoff, target, input.dryRun)
		if gcErr != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to run garbage collection", "gc の実行に失敗しました"), gcErr)
		}
	}

	if input.dryRun {
		if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Orphan refinement candidates", "orphan 機械要約候補"), orphanResult.ProducedCount()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dry-run result", "dry-run 結果の出力に失敗しました"), err)
		}
		if orphanResult.Skipped() > 0 {
			if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Orphan ranges skipped", "orphan range のスキップ"), orphanResult.Skipped()); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print dry-run result", "dry-run 結果の出力に失敗しました"), err)
			}
		}
		if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Candidates", "対象候補"), result.DeletedCount()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dry-run result", "dry-run 結果の出力に失敗しました"), err)
		}
		if consolidationApplied && !orphanResult.Complete() {
			if _, err := fmt.Fprintf(output, "%s\n", Localize(
				"More orphan ranges remain; re-run gc to continue consolidation",
				"orphan range が残っています。機械要約を続けるには gc を再実行してください",
			)); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print dry-run result", "dry-run 結果の出力に失敗しました"), err)
			}
		}
		return nil
	}

	if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Orphan refinements", "orphan 機械要約"), orphanResult.ProducedCount()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
	}
	if orphanResult.Skipped() > 0 {
		if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Orphan ranges skipped", "orphan range のスキップ"), orphanResult.Skipped()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
		}
	}
	if skipDeletion {
		if _, err := fmt.Fprintf(output, "%s\n", Localize(
			"Cleanup skipped: orphan ranges are not fully consolidated; re-run gc to continue",
			"整理をスキップしました: orphan range の機械要約が未完了です。gc を再実行してください",
		)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
		}
		return nil
	}
	// The count means different things per target, so the label has to as
	// well: events only discards bodies, all mixes discards with deletions,
	// and the remaining targets still delete rows.
	label := Localize("Deleted", "削除しました")
	switch target {
	case apptypes.GarbageCollectionTargetEvents:
		label = Localize("Discarded bodies", "破棄した本文")
	case apptypes.GarbageCollectionTargetAll:
		label = Localize("Collected", "整理しました")
	}
	if _, err := fmt.Fprintf(output, "%s: %d\n", label, result.DeletedCount()); err != nil {
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
