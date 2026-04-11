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
	"github.com/duck8823/traceary/application/usecase"
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

			_, err := resolveDBPath(dbPath)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
			}

			if err := c.storeMaintenance.Initialize(ctx); err != nil {
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
			if fromTime.IsPresent() && toTime.IsPresent() && fromTime.Get().After(toTime.Get()) {
				return xerrors.Errorf(Localize("--from must be earlier than --to", "from は to より前である必要があります"))
			}

			resolvedRepo := resolveWorkspaceValue(ctx, repo)

			summaries, err := c.session.List(ctx, usecase.SessionListCriteria{
				Limit:  limit,
				Offset: offset,
				Workspace: types.Workspace(resolvedRepo),
				Client: types.Client(client),
				Agent: types.Agent(agent),
				Label:  label,
				From:   fromTime,
				To:     toTime,
			})
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
	listCmd.Flags().StringVar(&from, "from", "", Localize("start date (YYYY-MM-DD or RFC3339)", "開始日 (YYYY-MM-DD または RFC3339)"))
	listCmd.Flags().StringVar(&to, "to", "", Localize("end date (YYYY-MM-DD or RFC3339)", "終了日 (YYYY-MM-DD または RFC3339)"))
	listCmd.Flags().StringVar(&since, "since", "", Localize("start date (alias for --from)", "開始日 (--from の別名)"))
	listCmd.Flags().StringVar(&until, "until", "", Localize("end date (alias for --to)", "終了日 (--to の別名)"))
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
		if s.EndedAt().IsPresent() {
			duration = formatDuration(s.EndedAt().Get().Sub(s.StartedAt()))
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
	type jsonSummary struct {
		SessionID       string   `json:"session_id"`
		Workspace string   `json:"workspace,omitempty"`
		Label           string   `json:"label,omitempty"`
		Summary         string   `json:"summary,omitempty"`
		ParentSessionID string   `json:"parent_session_id,omitempty"`
		StartedAt       string   `json:"started_at"`
		EndedAt         *string  `json:"ended_at,omitempty"`
		Status          string   `json:"status"`
		DurationSec     *float64 `json:"duration_sec,omitempty"`
		TotalEvents     int      `json:"total_events"`
		CommandCount    int      `json:"command_count"`
		Agents          []string `json:"agents"`
	}

	items := make([]jsonSummary, 0, len(summaries))
	for _, s := range summaries {
		item := jsonSummary{
			SessionID: string(s.SessionID()),
			Workspace: string(s.Workspace()),
			Label:           s.Label(),
			Summary:         s.Summary(),
			ParentSessionID: string(s.ParentSessionID()),
			StartedAt:       s.StartedAt().UTC().Format(time.RFC3339),
			Status:          s.Status(),
			TotalEvents:     s.TotalEvents(),
			CommandCount:    s.CommandCount(),
			Agents:          s.Agents(),
		}
		if s.EndedAt().IsPresent() {
			endStr := s.EndedAt().Get().UTC().Format(time.RFC3339)
			item.EndedAt = &endStr
			dur := s.EndedAt().Get().Sub(s.StartedAt()).Seconds()
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
