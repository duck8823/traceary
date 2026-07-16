package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionListCommand() *cobra.Command {
	var (
		dbPath string
		repo   string
		client string
		agent  string
		label  string
		from   string
		to     string
		since  string
		until  string
		limit  int
		offset int
		asJSON bool
	)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: Localize("List session summaries", "セッション一覧を表示する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			output := cmd.OutOrStdout()

			resolvedDBPath, err := resolveDBPath(dbPath)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
			}
			c.applyDatabasePath(resolvedDBPath)

			if err := c.storeManagement.Initialize(ctx); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
			}

			if offset < 0 {
				return xerrors.Errorf("%s", Localize("offset must be >= 0", "offset は 0 以上でなければなりません"))
			}

			fromValue, err := resolveSearchDateValue(from, since, "from", "since")
			if err != nil {
				return err
			}
			toValue, err := resolveSearchDateValue(to, until, "to", "until")
			if err != nil {
				return err
			}
			fromTime, err := parseFlexibleTimeOptional(fromValue, false)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve --from", "from の解決に失敗しました"), err)
			}
			toTime, err := parseFlexibleTimeOptional(toValue, false)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve --to", "to の解決に失敗しました"), err)
			}
			if fromVal, fromOk := fromTime.Value(); fromOk {
				if toVal, toOk := toTime.Value(); toOk && fromVal.After(toVal) {
					return xerrors.New(Localize("--from must be earlier than --to", "from は to より前である必要があります"))
				}
			}

			resolvedRepo := resolveWorkspaceValue(ctx, repo)

			criteria := apptypes.NewSessionListCriteriaBuilder(limit).
				Offset(offset).
				Workspace(types.Workspace(resolvedRepo)).
				Client(types.Client(client)).
				Agent(types.Agent(agent)).
				Label(label).
				From(fromTime).
				To(toTime).
				Build()
			summaries, err := c.session.List(ctx, criteria)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to list sessions", "セッション一覧の取得に失敗しました"), err)
			}

			return writeSessionSummaries(output, summaries, asJSON)
		},
	}

	listCmd.Flags().StringVar(&dbPath, "db-path", "", Localize("SQLite DB path (env: TRACEARY_DB_PATH)", "SQLite DB パス (env: TRACEARY_DB_PATH)"))
	listCmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	listCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "記録経路でフィルタ"))
	listCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "エージェントでフィルタ"))
	listCmd.Flags().StringVar(&label, "label", "", Localize("filter by label", "ラベルでフィルタ"))
	listCmd.Flags().StringVar(&from, "from", "", Localize("start date (YYYY-MM-DD or RFC3339; alias: --since)", "開始日 (YYYY-MM-DD または RFC3339; 別名: --since)"))
	listCmd.Flags().StringVar(&to, "to", "", Localize("end date (YYYY-MM-DD or RFC3339; alias: --until)", "終了日 (YYYY-MM-DD または RFC3339; 別名: --until)"))
	listCmd.Flags().StringVar(&since, "since", "", Localize("start date (YYYY-MM-DD or RFC3339; alias for --from)", "開始日 (YYYY-MM-DD または RFC3339; --from の別名)"))
	listCmd.Flags().StringVar(&until, "until", "", Localize("end date (YYYY-MM-DD or RFC3339; alias for --to)", "終了日 (YYYY-MM-DD または RFC3339; --to の別名)"))
	listCmd.Flags().IntVar(&limit, "limit", 20, Localize("maximum number of sessions", "最大表示セッション数"))
	listCmd.Flags().IntVar(&offset, "offset", 0, Localize("number of sessions to skip", "スキップするセッション数"))
	listCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return listCmd
}

func writeSessionSummaries(output io.Writer, summaries []apptypes.SessionSummary, asJSON bool) error {
	if asJSON {
		return writeSessionSummariesJSON(output, summaries)
	}

	if len(summaries) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No sessions found.", "セッションが見つかりません")); err != nil {
			return xerrors.Errorf("failed to print empty message: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintln(
		output,
		"STARTED_AT\tSTATUS\tDURATION\tSESSION_ID\tWORKSPACE\tLABEL\tSUMMARY\tPARENT_SESSION_ID\tEVENTS\tCMDS\tAGENTS",
	); err != nil {
		return xerrors.Errorf("failed to print header: %w", err)
	}
	for _, s := range summaries {
		duration := "-"
		if endedAt, ok := s.EndedAt().Value(); ok {
			duration = formatDuration(endedAt.Sub(s.StartedAt()))
		}

		agentSummary := strings.Join(s.Agents(), ", ")

		if _, err := fmt.Fprintf(
			output,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			s.StartedAt().UTC().Format("2006-01-02T15:04:05Z"),
			s.Status(),
			duration,
			s.SessionID(),
			formatOptionalColumn(s.Workspace().String()),
			formatOptionalColumn(normalizeTabularColumn(s.Label())),
			formatOptionalColumn(truncateMessage(s.Summary())),
			formatOptionalColumn(normalizeTabularColumn(s.ParentSessionID().String())),
			s.TotalEvents(),
			s.CommandCount(),
			agentSummary,
		); err != nil {
			return xerrors.Errorf("failed to print session row: %w", err)
		}
	}

	return nil
}

func writeSessionSummariesJSON(output io.Writer, summaries []apptypes.SessionSummary) error {
	items := make([]sessionSummaryOutput, 0, len(summaries))
	for _, s := range summaries {
		item := sessionSummaryOutput{
			SessionID:       string(s.SessionID()),
			Workspace:       string(s.Workspace()),
			Label:           s.Label(),
			Summary:         s.Summary(),
			Model:           s.Model(),
			ParentSessionID: string(s.ParentSessionID()),
			SpawnEventID:    s.SpawnEventID().String(),
			SubagentKind:    s.SubagentKind(),
			StartedAt:       formatJSONTime(s.StartedAt()),
			Status:          s.Status(),
			TotalEvents:     s.TotalEvents(),
			CommandCount:    s.CommandCount(),
			Agents:          s.Agents(),
		}
		if spawnOrder, ok := s.SpawnOrder().Value(); ok {
			item.SpawnOrder = &spawnOrder
		}
		if endedAt, ok := s.EndedAt().Value(); ok {
			endStr := formatJSONTime(endedAt)
			item.EndedAt = &endStr
			dur := endedAt.Sub(s.StartedAt()).Seconds()
			item.DurationSec = &dur
		}
		items = append(items, item)
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(items); err != nil {
		return xerrors.Errorf("failed to encode JSON: %w", err)
	}

	return nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
