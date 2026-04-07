package cli

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
)

func (c *RootCLI) newSearchCommand() *cobra.Command {
	var (
		dbPath string
		repo   string
		from   string
		to     string
		limit  int
		asJSON bool
	)

	searchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "記録を検索する",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runSearch(cmd.Context(), cmd.OutOrStdout(), searchCommandInput{
				dbPath: dbPath,
				repo:   repo,
				from:   from,
				to:     to,
				limit:  limit,
				query:  args[0],
				asJSON: asJSON,
			})
		},
	}
	searchCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	searchCmd.Flags().StringVar(&repo, "repo", "", "絞り込む work context (env: TRACEARY_REPO / current git remote)")
	searchCmd.Flags().StringVar(&from, "from", "", "開始日 (`YYYY-MM-DD`)")
	searchCmd.Flags().StringVar(&to, "to", "", "終了日 (`YYYY-MM-DD`)")
	searchCmd.Flags().IntVar(&limit, "limit", 20, "表示件数")
	searchCmd.Flags().BoolVar(&asJSON, "json", false, "JSON 形式で出力する")

	return searchCmd
}

type searchCommandInput struct {
	dbPath string
	repo   string
	from   string
	to     string
	limit  int
	query  string
	asJSON bool
}

func (c *RootCLI) runSearch(ctx context.Context, output io.Writer, input searchCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.searchEventsQueryService == nil {
		return xerrors.Errorf("検索クエリサービスが設定されていません")
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	fromTime, err := parseSearchDate(input.from, false)
	if err != nil {
		return xerrors.Errorf("from の解決に失敗しました: %w", err)
	}
	toTime, err := parseSearchDate(input.to, true)
	if err != nil {
		return xerrors.Errorf("to の解決に失敗しました: %w", err)
	}

	events, err := c.searchEventsQueryService.Run(ctx, resolvedPath, queryservice.SearchEventsInput{
		Query: input.query,
		Repo:  resolveRepoValue(ctx, input.repo),
		From:  fromTime,
		To:    toTime,
		Limit: input.limit,
	})
	if err != nil {
		return xerrors.Errorf("検索に失敗しました: %w", err)
	}

	if err := writeEventsByFormat(output, events, input.asJSON); err != nil {
		return xerrors.Errorf("検索結果の出力に失敗しました: %w", err)
	}

	return nil
}

func parseSearchDate(value string, endExclusive bool) (time.Time, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return time.Time{}, nil
	}

	parsedTime, err := time.Parse("2006-01-02", trimmedValue)
	if err != nil {
		return time.Time{}, xerrors.Errorf("日付は YYYY-MM-DD 形式で指定してください: %w", err)
	}
	if endExclusive {
		return parsedTime.AddDate(0, 0, 1), nil
	}

	return parsedTime, nil
}
