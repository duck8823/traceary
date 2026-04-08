package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

const defaultRetentionDays = 90

var gcNowFunc = time.Now

func (c *RootCLI) newGCCommand() *cobra.Command {
	var (
		dbPath   string
		keepDays int
		dryRun   bool
	)

	gcCmd := &cobra.Command{
		Use:   "gc",
		Short: Localize("Delete old events and compact the store", "古いイベントを削除してストアを最適化する"),
		Args:  noArgsJP(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runGC(cmd.Context(), cmd.OutOrStdout(), gcCommandInput{
				dbPath:   dbPath,
				keepDays: keepDays,
				dryRun:   dryRun,
			})
		},
	}
	gcCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage)
	gcCmd.Flags().IntVar(&keepDays, "keep-days", defaultRetentionDays, Localize("number of days to retain", "保持する日数"))
	gcCmd.Flags().BoolVar(&dryRun, "dry-run", false, Localize("print the number of candidate records only", "削除対象件数のみ表示する"))

	return gcCmd
}

type gcCommandInput struct {
	dbPath   string
	keepDays int
	dryRun   bool
}

func (c *RootCLI) runGC(ctx context.Context, output io.Writer, input gcCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.collectGarbageUsecase == nil {
		return xerrors.Errorf(Localize("garbage collection usecase is not configured", "gc ユースケースが設定されていません"))
	}
	if input.keepDays <= 0 {
		return xerrors.Errorf(Localize("--keep-days must be greater than or equal to 1", "keep-days は 1 以上である必要があります"))
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	cutoff := gcNowFunc().AddDate(0, 0, -input.keepDays)
	result, err := c.collectGarbageUsecase.Run(ctx, usecase.CollectGarbageInput{
		DBPath: resolvedPath,
		Before: cutoff,
		DryRun: input.dryRun,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to run garbage collection", "gc の実行に失敗しました"), err)
	}

	if result.DryRun {
		if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Candidates", "削除対象"), result.DeletedCount); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dry-run result", "dry-run 結果の出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Deleted", "削除しました"), result.DeletedCount); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print gc result", "gc 結果の出力に失敗しました"), err)
	}

	return nil
}
