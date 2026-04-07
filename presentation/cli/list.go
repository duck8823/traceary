package cli

import (
	"context"
	"fmt"
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
	if len(events) == 0 {
		if _, err := fmt.Fprintln(output, "記録はまだありません"); err != nil {
			return xerrors.Errorf("空一覧メッセージの出力に失敗しました: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintln(output, "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tREPO\tMESSAGE"); err != nil {
		return xerrors.Errorf("一覧ヘッダーの出力に失敗しました: %w", err)
	}
	for _, event := range events {
		if _, err := fmt.Fprintf(
			output,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			event.CreatedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
			event.Kind(),
			formatOptionalColumn(event.Client()),
			event.Agent(),
			event.SessionID(),
			formatOptionalColumn(event.Repo()),
			event.Body(),
		); err != nil {
			return xerrors.Errorf("イベント一覧行の出力に失敗しました: %w", err)
		}
	}

	return nil
}

func formatOptionalColumn(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
