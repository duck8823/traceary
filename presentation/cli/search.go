package cli

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSearchCommand() *cobra.Command {
	var (
		dbPath       string
		repo         string
		sessionID    string
		client       string
		agent        string
		kind         string
		from         string
		since        string
		to           string
		until        string
		limit        int
		offset       int
		failuresOnly bool
		asJSON       bool
		wide         bool
		utc          bool
		fields       []string
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
				dbPath:       dbPath,
				repo:         repo,
				sessionID:    sessionID,
				client:       client,
				agent:        agent,
				kind:         kind,
				from:         from,
				since:        since,
				to:           to,
				until:        until,
				limit:        limit,
				offset:       offset,
				query:        query,
				failuresOnly: failuresOnly,
				asJSON:       asJSON,
				wide:         wide,
				utc:          utc,
				fields:       fields,
				fieldsSet:    cmd.Flags().Changed("fields"),
			})
		},
	}
	searchCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	searchCmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by workspace (env: TRACEARY_WORKSPACE / current git remote)", "絞り込む workspace (env: TRACEARY_WORKSPACE / current git remote)"))
	searchCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("filter by session ID", "絞り込む session ID"))
	searchCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "絞り込む client"))
	searchCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "絞り込む agent"))
	searchCmd.Flags().StringVar(
		&kind,
		"kind",
		"",
		Localize(
			"filter by event kind (note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt; alias: audit)",
			"絞り込む kind (note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt; alias: audit)",
		),
	)
	searchCmd.Flags().StringVar(&from, "from", "", Localize("start date (`YYYY-MM-DD` or RFC3339)", "開始日 (`YYYY-MM-DD` または RFC3339)"))
	searchCmd.Flags().StringVar(&since, "since", "", Localize("start date (`YYYY-MM-DD` or RFC3339) (alias for `--from`)", "開始日 (`YYYY-MM-DD` または RFC3339) (`--from` の別名)"))
	searchCmd.Flags().StringVar(&to, "to", "", Localize("end date (`YYYY-MM-DD` or RFC3339)", "終了日 (`YYYY-MM-DD` または RFC3339)"))
	searchCmd.Flags().StringVar(&until, "until", "", Localize("end date (`YYYY-MM-DD` or RFC3339) (alias for `--to`)", "終了日 (`YYYY-MM-DD` または RFC3339) (`--to` の別名)"))
	searchCmd.Flags().IntVar(&limit, "limit", 20, Localize("maximum number of results", "表示件数"))
	searchCmd.Flags().IntVar(&offset, "offset", 0, Localize("number of matching events to skip before returning results", "結果を返す前にスキップする件数"))
	searchCmd.Flags().BoolVar(&failuresOnly, "failures", false, Localize("show only failed commands", "失敗したコマンドのみ表示"))
	searchCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	searchCmd.Flags().BoolVar(&wide, "wide", false, Localize("use the legacy tab-separated seven-column format", "従来のタブ区切り 7 カラム形式で出力する"))
	searchCmd.Flags().BoolVar(&utc, "utc", false, Localize("print text timestamps in UTC instead of local time", "テキスト出力のタイムスタンプを現地時刻ではなく UTC で出力する"))
	searchCmd.Flags().StringSliceVar(&fields, "fields", nil, readFieldsFlagUsage())

	return searchCmd
}

func (c *RootCLI) runSearch(ctx context.Context, output io.Writer, input searchCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.Errorf(Localize("search events query service is not configured", "検索クエリサービスが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.Errorf(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	if input.offset < 0 {
		return xerrors.Errorf(Localize("offset must be greater than or equal to 0", "offset は 0 以上である必要があります"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
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
	if !hasSearchConstraint(input.query, input.repo, input.sessionID, input.client, input.agent, input.kind, fromTime, toTime, input.failuresOnly) {
		return xerrors.Errorf(Localize("at least one search filter is required", "検索条件は1つ以上必要です"))
	}
	if !fromTime.IsZero() && !toTime.IsZero() && fromTime.After(toTime) {
		return xerrors.Errorf(Localize("--from must be earlier than --to", "from は to より前である必要があります"))
	}
	resolvedKind, err := validateSearchKind(input.kind)
	if err != nil {
		return err
	}

	criteria := apptypes.NewEventSearchCriteriaBuilder(input.limit).
		Query(input.query).
		Workspace(types.Workspace(resolveWorkspaceValue(ctx, input.repo))).
		SessionID(types.SessionID(input.sessionID)).
		Client(types.Client(input.client)).
		Agent(types.Agent(input.agent)).
		Kind(types.EventKind(resolvedKind)).
		From(fromTime).
		To(toTime).
		Offset(input.offset).
		FailuresOnly(input.failuresOnly).
		Build()
	events, err := c.event.Search(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to search events", "検索に失敗しました"), err)
	}

	resolvedFields, err := c.resolveReadFieldsForCommand(input.fields, input.fieldsSet, input.wide)
	if err != nil {
		return err
	}
	textOpts := eventTextFormatOptions{
		wide:     input.wide,
		utc:      input.utc,
		location: input.location,
		fields:   resolvedFields,
	}
	extrasFor := c.makeCompactExtrasResolver(ctx, resolvedFields)
	if err := writeEventsByFormat(output, events, input.asJSON, textOpts, extrasFor); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print search results", "検索結果の出力に失敗しました"), err)
	}

	return nil
}

// parseSearchDate delegates to parseFlexibleTime, which accepts both
// RFC3339 and YYYY-MM-DD formats.
func parseSearchDate(value string, endExclusive bool) (time.Time, error) {
	return parseFlexibleTime(value, endExclusive)
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
	failuresOnly bool,
) bool {
	return strings.TrimSpace(query) != "" ||
		strings.TrimSpace(repo) != "" ||
		strings.TrimSpace(sessionID) != "" ||
		strings.TrimSpace(client) != "" ||
		strings.TrimSpace(agent) != "" ||
		strings.TrimSpace(kind) != "" ||
		!from.IsZero() ||
		!to.IsZero() ||
		failuresOnly
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
		"compact_summary":  "compact_summary",
		"prompt":           "prompt",
		"audit":            "command_executed",
	}
	if resolvedKind, ok := validKinds[trimmedValue]; ok {
		return resolvedKind, nil
	}

	return "", xerrors.Errorf(Localize(
		"unsupported kind: %s (valid values: note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt; alias: audit)",
		"未対応の kind です: %s (有効値: note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt; alias: audit)",
	), trimmedValue)
}
