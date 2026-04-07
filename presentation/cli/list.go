package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newListCommand() *cobra.Command {
	var (
		dbPath string
		limit  int
	)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "直近のログを一覧表示する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runList(cmd.Context(), cmd.OutOrStdout(), dbPath, limit)
		},
	}
	listCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	listCmd.Flags().IntVar(&limit, "limit", 20, "表示件数")

	return listCmd
}

func (c *RootCLI) runList(ctx context.Context, output io.Writer, dbPath string, limit int) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.listEventsQueryService == nil {
		return xerrors.Errorf("イベント一覧クエリサービスが設定されていません")
	}

	resolvedPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	events, err := c.listEventsQueryService.Run(ctx, resolvedPath, limit)
	if err != nil {
		return xerrors.Errorf("イベント一覧の取得に失敗しました: %w", err)
	}
	if err := writeEvents(output, events); err != nil {
		return xerrors.Errorf("一覧出力に失敗しました: %w", err)
	}

	return nil
}
