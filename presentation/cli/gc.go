package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

const defaultRetentionDays = 90

var gcNowFunc = time.Now

func (c *RootCLI) newGCCommand() *cobra.Command {
	return c.newGCCommandWithDeprecation(Localize(
		"use `traceary store gc` — the top-level alias will be removed in v1.0",
		"`traceary store gc` を使ってください — この top-level alias は v1.0 で削除されます",
	))
}

func (c *RootCLI) newStoreGCCommand() *cobra.Command {
	return c.newGCCommandWithDeprecation("")
}

func (c *RootCLI) newGCCommandWithDeprecation(deprecated string) *cobra.Command {
	var (
		dbPath   string
		keepDays int
		dryRun   bool
	)

	gcCmd := &cobra.Command{
		Use:   "gc",
		Short: Localize("Delete old events and compact the store", "古いイベントを削除してストアを最適化する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runGC(cmd.Context(), cmd.OutOrStdout(), gcCommandInput{
				dbPath:   dbPath,
				keepDays: keepDays,
				dryRun:   dryRun,
			})
		},
	}
	gcCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	gcCmd.Flags().IntVar(&keepDays, "keep-days", defaultRetentionDays, Localize("number of days to retain", "保持する日数"))
	gcCmd.Flags().BoolVar(&dryRun, "dry-run", false, Localize("print the number of candidate records only", "削除対象件数のみ表示する"))
	if deprecated != "" {
		applyDeprecation(gcCmd, deprecated)
	}

	return gcCmd
}

func (c *RootCLI) runGC(ctx context.Context, output io.Writer, input gcCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	if input.keepDays <= 0 {
		return xerrors.Errorf(Localize("--keep-days must be greater than or equal to 1", "keep-days は 1 以上である必要があります"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	cutoff := gcNowFunc().AddDate(0, 0, -input.keepDays)
	result, err := c.storeManagement.CollectGarbage(ctx, cutoff, input.dryRun)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to run garbage collection", "gc の実行に失敗しました"), err)
	}

	if result.DryRun() {
		if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Candidates", "削除対象"), result.DeletedCount()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dry-run result", "dry-run 結果の出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Deleted", "削除しました"), result.DeletedCount()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
	}

	return nil
}
