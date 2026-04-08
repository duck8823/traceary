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
		dbPath    string
		repo      string
		sessionID string
		client    string
		agent     string
		kind      string
		from      string
		since     string
		to        string
		until     string
		limit     int
		asJSON    bool
	)

	searchCmd := &cobra.Command{
		Use:   "search [query]",
		Short: "記録を検索する",
		Args:  maximumNArgsJP(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			return c.runSearch(cmd.Context(), cmd.OutOrStdout(), searchCommandInput{
				dbPath:    dbPath,
				repo:      repo,
				sessionID: sessionID,
				client:    client,
				agent:     agent,
				kind:      kind,
				from:      from,
				since:     since,
				to:        to,
				until:     until,
				limit:     limit,
				query:     query,
				asJSON:    asJSON,
			})
		},
	}
	searchCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage)
	searchCmd.Flags().StringVar(&repo, "repo", "", "絞り込む work context (env: TRACEARY_REPO / current git remote)")
	searchCmd.Flags().StringVar(&sessionID, "session-id", "", "絞り込む session ID")
	searchCmd.Flags().StringVar(&client, "client", "", "絞り込む client")
	searchCmd.Flags().StringVar(&agent, "agent", "", "絞り込む agent")
	searchCmd.Flags().StringVar(
		&kind,
		"kind",
		"",
		"絞り込む kind (note, command_executed, reviewed, session_started, session_ended; alias: audit)",
	)
	searchCmd.Flags().StringVar(&from, "from", "", "開始日 (`YYYY-MM-DD`)")
	searchCmd.Flags().StringVar(&since, "since", "", "開始日 (`YYYY-MM-DD`) (`--from` の別名)")
	searchCmd.Flags().StringVar(&to, "to", "", "終了日 (`YYYY-MM-DD`)")
	searchCmd.Flags().StringVar(&until, "until", "", "終了日 (`YYYY-MM-DD`) (`--to` の別名)")
	searchCmd.Flags().IntVar(&limit, "limit", 20, "表示件数")
	searchCmd.Flags().BoolVar(&asJSON, "json", false, "JSON 形式で出力する")

	return searchCmd
}

type searchCommandInput struct {
	dbPath    string
	repo      string
	sessionID string
	client    string
	agent     string
	kind      string
	from      string
	since     string
	to        string
	until     string
	limit     int
	query     string
	asJSON    bool
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

	fromValue, err := resolveSearchDateValue(input.from, input.since, "from", "since")
	if err != nil {
		return err
	}
	toValue, err := resolveSearchDateValue(input.to, input.until, "to", "until")
	if err != nil {
		return err
	}

	fromTime, err := parseSearchDate(fromValue, false)
	if err != nil {
		return xerrors.Errorf("from の解決に失敗しました: %w", err)
	}
	toTime, err := parseSearchDate(toValue, true)
	if err != nil {
		return xerrors.Errorf("to の解決に失敗しました: %w", err)
	}

	events, err := c.searchEventsQueryService.Run(ctx, resolvedPath, queryservice.SearchEventsInput{
		Query:     input.query,
		Repo:      resolveRepoValue(ctx, input.repo),
		SessionID: input.sessionID,
		Client:    input.client,
		Agent:     input.agent,
		Kind:      input.kind,
		From:      fromTime,
		To:        toTime,
		Limit:     input.limit,
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

func resolveSearchDateValue(primary string, alias string, primaryName string, aliasName string) (string, error) {
	trimmedPrimary := strings.TrimSpace(primary)
	trimmedAlias := strings.TrimSpace(alias)
	if trimmedPrimary == "" {
		return trimmedAlias, nil
	}
	if trimmedAlias == "" {
		return trimmedPrimary, nil
	}
	if trimmedPrimary != trimmedAlias {
		return "", xerrors.Errorf("%s と %s に異なる日付は指定できません", primaryName, aliasName)
	}

	return trimmedPrimary, nil
}
