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
		offset    int
		asJSON    bool
	)

	searchCmd := &cobra.Command{
		Use:   "search [query]",
		Short: Localize("Search recorded events", "記録を検索する"),
		Args:  maximumNArgsLocalized(1),
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
				offset:    offset,
				query:     query,
				asJSON:    asJSON,
			})
		},
	}
	searchCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	searchCmd.Flags().StringVar(&repo, "repo", "", Localize("filter by work context (env: TRACEARY_REPO / current git remote)", "絞り込む work context (env: TRACEARY_REPO / current git remote)"))
	searchCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("filter by session ID", "絞り込む session ID"))
	searchCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "絞り込む client"))
	searchCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "絞り込む agent"))
	searchCmd.Flags().StringVar(
		&kind,
		"kind",
		"",
		Localize(
			"filter by event kind (note, command_executed, reviewed, session_started, session_ended; alias: audit)",
			"絞り込む kind (note, command_executed, reviewed, session_started, session_ended; alias: audit)",
		),
	)
	searchCmd.Flags().StringVar(&from, "from", "", Localize("start date (`YYYY-MM-DD`)", "開始日 (`YYYY-MM-DD`)"))
	searchCmd.Flags().StringVar(&since, "since", "", Localize("start date (`YYYY-MM-DD`) (alias for `--from`)", "開始日 (`YYYY-MM-DD`) (`--from` の別名)"))
	searchCmd.Flags().StringVar(&to, "to", "", Localize("end date (`YYYY-MM-DD`)", "終了日 (`YYYY-MM-DD`)"))
	searchCmd.Flags().StringVar(&until, "until", "", Localize("end date (`YYYY-MM-DD`) (alias for `--to`)", "終了日 (`YYYY-MM-DD`) (`--to` の別名)"))
	searchCmd.Flags().IntVar(&limit, "limit", 20, Localize("maximum number of results", "表示件数"))
	searchCmd.Flags().IntVar(&offset, "offset", 0, Localize("number of matching events to skip before returning results", "結果を返す前にスキップする件数"))
	searchCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

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
	offset    int
	query     string
	asJSON    bool
}

func (c *RootCLI) runSearch(ctx context.Context, output io.Writer, input searchCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.searchEventsQueryService == nil {
		return xerrors.Errorf(Localize("search events query service is not configured", "検索クエリサービスが設定されていません"))
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
		return xerrors.Errorf("%s: %w", Localize("failed to resolve --from", "from の解決に失敗しました"), err)
	}
	toTime, err := parseSearchDate(toValue, true)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve --to", "to の解決に失敗しました"), err)
	}
	if !hasSearchConstraint(input.query, input.repo, input.sessionID, input.client, input.agent, input.kind, fromTime, toTime) {
		return xerrors.Errorf(Localize("at least one search filter is required", "検索条件は1つ以上必要です"))
	}
	if !fromTime.IsZero() && !toTime.IsZero() && fromTime.After(toTime) {
		return xerrors.Errorf(Localize("--from must be earlier than --to", "from は to より前である必要があります"))
	}
	resolvedKind, err := validateSearchKind(input.kind)
	if err != nil {
		return err
	}

	events, err := c.searchEventsQueryService.Run(ctx, resolvedPath, queryservice.SearchEventsInput{
		Query:     input.query,
		Repo:      resolveRepoValue(ctx, input.repo),
		SessionID: input.sessionID,
		Client:    input.client,
		Agent:     input.agent,
		Kind:      resolvedKind,
		From:      fromTime,
		To:        toTime,
		Limit:     input.limit,
		Offset:    input.offset,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to search events", "検索に失敗しました"), err)
	}

	if err := writeEventsByFormat(output, events, input.asJSON); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print search results", "検索結果の出力に失敗しました"), err)
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
		return time.Time{}, xerrors.Errorf("%s: %w", Localize("date must use YYYY-MM-DD format", "日付は YYYY-MM-DD 形式で指定してください"), err)
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
		return "", xerrors.Errorf(localizef("%s and %s must match when both are set", "%s と %s に異なる日付は指定できません", primaryName, aliasName))
	}

	return trimmedPrimary, nil
}

func hasSearchConstraint(
	query string,
	repo string,
	sessionID string,
	client string,
	agent string,
	kind string,
	from time.Time,
	to time.Time,
) bool {
	return strings.TrimSpace(query) != "" ||
		strings.TrimSpace(repo) != "" ||
		strings.TrimSpace(sessionID) != "" ||
		strings.TrimSpace(client) != "" ||
		strings.TrimSpace(agent) != "" ||
		strings.TrimSpace(kind) != "" ||
		!from.IsZero() ||
		!to.IsZero()
}

func validateSearchKind(value string) (string, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "", nil
	}

	validKinds := map[string]string{
		"note":             "note",
		"command_executed": "command_executed",
		"reviewed":         "reviewed",
		"session_started":  "session_started",
		"session_ended":    "session_ended",
		"audit":            "command_executed",
	}
	if resolvedKind, ok := validKinds[trimmedValue]; ok {
		return resolvedKind, nil
	}

	return "", xerrors.Errorf(Localize(
		"unsupported kind: %s (valid values: note, command_executed, reviewed, session_started, session_ended; alias: audit)",
		"未対応の kind です: %s (有効値: note, command_executed, reviewed, session_started, session_ended; alias: audit)",
	), trimmedValue)
}
