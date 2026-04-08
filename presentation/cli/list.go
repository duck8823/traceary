package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
)

func (c *RootCLI) newListCommand() *cobra.Command {
	var (
		dbPath string
		limit  int
		offset int
		asJSON bool
	)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: Localize("List recent events", "直近のログを一覧表示する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runList(cmd.Context(), cmd.OutOrStdout(), listCommandInput{
				dbPath: dbPath,
				limit:  limit,
				offset: offset,
				asJSON: asJSON,
			})
		},
	}
	listCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	listCmd.Flags().IntVar(&limit, "limit", 20, Localize("number of events to display", "表示件数"))
	listCmd.Flags().IntVar(&offset, "offset", 0, Localize("number of events to skip before listing", "一覧表示前にスキップする件数"))
	listCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return listCmd
}

type listCommandInput struct {
	dbPath string
	limit  int
	offset int
	asJSON bool
}

func (c *RootCLI) runList(ctx context.Context, output io.Writer, input listCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.listEventsQueryService == nil {
		return xerrors.Errorf(Localize("list events query service is not configured", "イベント一覧クエリサービスが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.Errorf(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	if input.offset < 0 {
		return xerrors.Errorf(Localize("offset must be greater than or equal to 0", "offset は 0 以上である必要があります"))
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	events, err := c.listEventsQueryService.Run(ctx, resolvedPath, queryservice.ListRecentEventsInput{
		Limit:  input.limit,
		Offset: input.offset,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list events", "イベント一覧の取得に失敗しました"), err)
	}
	if err := writeEventsByFormat(output, events, input.asJSON); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print event list", "一覧出力に失敗しました"), err)
	}

	return nil
}
