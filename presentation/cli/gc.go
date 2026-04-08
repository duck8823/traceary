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
		Short: "古いイベントを削除してストアを最適化する",
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
	gcCmd.Flags().IntVar(&keepDays, "keep-days", defaultRetentionDays, "保持する日数")
	gcCmd.Flags().BoolVar(&dryRun, "dry-run", false, "削除対象件数のみ表示する")

	return gcCmd
}

type gcCommandInput struct {
	dbPath   string
	keepDays int
	dryRun   bool
}

func (c *RootCLI) runGC(ctx context.Context, output io.Writer, input gcCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.collectGarbageUsecase == nil {
		return xerrors.Errorf("gc ユースケースが設定されていません")
	}
	if input.keepDays <= 0 {
		return xerrors.Errorf("keep-days は 1 以上である必要があります")
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	cutoff := gcNowFunc().AddDate(0, 0, -input.keepDays)
	result, err := c.collectGarbageUsecase.Run(ctx, usecase.CollectGarbageInput{
		DBPath: resolvedPath,
		Before: cutoff,
		DryRun: input.dryRun,
	})
	if err != nil {
		return xerrors.Errorf("gc の実行に失敗しました: %w", err)
	}

	if result.DryRun {
		if _, err := fmt.Fprintf(output, "削除対象: %d 件\n", result.DeletedCount); err != nil {
			return xerrors.Errorf("dry-run 結果の出力に失敗しました: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "削除しました: %d 件\n", result.DeletedCount); err != nil {
		return xerrors.Errorf("gc 結果の出力に失敗しました: %w", err)
	}

	return nil
}
