package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const (
	defaultGapMinutes      = 15
	timelineSummaryMaxRune = 72
)

func (c *RootCLI) newTimelineCommand() *cobra.Command {
	var (
		dbPath    string
		workspace string
		from      string
		since     string
		to        string
		until     string
		gap       int
		limit     int
		asJSON    bool
		utc       bool
	)

	cmd := &cobra.Command{
		Use:   "timeline",
		Short: Localize("Show work timeline with gap-based block detection", "ギャップ検出による作業タイムラインを表示する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runTimeline(cmd.Context(), cmd.OutOrStdout(), timelineCommandInput{
				dbPath:    dbPath,
				workspace: workspace,
				from:      from,
				since:     since,
				to:        to,
				until:     until,
				gap:       gap,
				limit:     limit,
				asJSON:    asJSON,
				utc:       utc,
			})
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&workspace, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().StringVar(&from, "from", "", Localize("start date (YYYY-MM-DD or RFC3339; alias: --since)", "開始日 (YYYY-MM-DD または RFC3339; 別名: --since)"))
	cmd.Flags().StringVar(&since, "since", "", Localize("start date (alias for --from)", "開始日 (--from の別名)"))
	cmd.Flags().StringVar(&to, "to", "", Localize("end date (YYYY-MM-DD or RFC3339; alias: --until)", "終了日 (YYYY-MM-DD または RFC3339; 別名: --until)"))
	cmd.Flags().StringVar(&until, "until", "", Localize("end date (alias for --to)", "終了日 (--to の別名)"))
	cmd.Flags().IntVar(&gap, "gap", defaultGapMinutes, Localize("idle gap threshold in minutes", "アイドル判定の閾値（分）"))
	cmd.Flags().IntVar(&limit, "limit", 20, Localize("maximum number of blocks to display", "表示するブロック数の上限"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.Flags().BoolVar(&utc, "utc", false, Localize("print text timestamps in UTC instead of local time", "テキスト出力のタイムスタンプを現地時刻ではなく UTC で出力する"))

	return cmd
}

func (c *RootCLI) runTimeline(ctx context.Context, output io.Writer, input timelineCommandInput) error {
	if c.event == nil {
		return xerrors.Errorf(Localize("event usecase is not configured", "イベントユースケースが設定されていません"))
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
	fromTime, err := parseDateFlag(fromValue)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("invalid --from value", "--from の値が不正です"), err)
	}
	toTime, err := parseDateFlag(toValue)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("invalid --to value", "--to の値が不正です"), err)
	}

	criteria := apptypes.NewTimelineCriteriaBuilder(input.limit).
		Workspace(types.Workspace(strings.TrimSpace(input.workspace))).
		From(fromTime).
		To(toTime).
		GapSeconds(input.gap * 60).
		Build()
	blocks, err := c.event.Timeline(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list timeline blocks", "タイムラインブロックの取得に失敗しました"), err)
	}

	if input.asJSON {
		return writeTimelineJSON(output, blocks)
	}

	textOpts := eventTextFormatOptions{utc: input.utc, location: input.location}
	return writeTimelineText(output, blocks, textOpts)
}

func computeKindCounts(kinds []string) map[string]int {
	counts := make(map[string]int)
	for _, k := range kinds {
		counts[k]++
	}
	return counts
}

func writeTimelineText(output io.Writer, blocks []apptypes.TimelineBlock, opts eventTextFormatOptions) error {
	if len(blocks) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No work blocks found.", "作業ブロックが見つかりませんでした。")); err != nil {
			return xerrors.Errorf("failed to print timeline: %w", err)
		}
		return nil
	}

	for _, b := range blocks {
		duration := b.BlockEnd().Sub(b.BlockStart())
		durationStr := formatDuration(duration)

		header := fmt.Sprintf(
			"%s - %s (%s) %s: %d",
			formatTextTimestamp(b.BlockStart(), opts, "2006-01-02 15:04"),
			formatTextTimestamp(b.BlockEnd(), opts, "15:04"),
			durationStr,
			Localize("total events", "総イベント数"),
			b.EventCount(),
		)
		if _, err := fmt.Fprintln(output, header); err != nil {
			return xerrors.Errorf("failed to print timeline block: %w", err)
		}

		breakdown := b.WorkspaceBreakdown()
		if len(breakdown) == 0 {
			continue
		}

		maxCountWidth := 0
		for _, ws := range breakdown {
			if w := countWidth(ws.EventCount()); w > maxCountWidth {
				maxCountWidth = w
			}
		}
		maxWorkspaceWidth := 0
		for _, ws := range breakdown {
			if w := runeLen(ws.Workspace()); w > maxWorkspaceWidth {
				maxWorkspaceWidth = w
			}
		}

		for _, ws := range breakdown {
			summary := workspaceActivityText(ws)
			line := fmt.Sprintf(
				"  %s (%s) — %s",
				padRight(ws.Workspace(), maxWorkspaceWidth),
				padLeft(fmt.Sprintf("%d", ws.EventCount()), maxCountWidth),
				summary,
			)
			if _, err := fmt.Fprintln(output, line); err != nil {
				return xerrors.Errorf("failed to print timeline block workspace row: %w", err)
			}
		}
	}
	return nil
}

// workspaceActivityText renders the per-workspace activity summary using
// the compact_summary → prompt → transcript → kind-counts fallback
// chain. The transcript case renders identically to prompt today (both
// are truncated free-form text); it is accepted here so the switch
// stays exhaustive with the timeline summary-source enum.
func workspaceActivityText(ws apptypes.TimelineWorkspaceBreakdown) string {
	switch ws.SummarySource() {
	case apptypes.TimelineSummarySourceCompactSummary,
		apptypes.TimelineSummarySourcePrompt,
		apptypes.TimelineSummarySourceTranscript:
		return truncateNormalized(ws.Summary(), timelineSummaryMaxRune)
	default:
		return formatKindCounts(computeKindCounts(ws.Kinds()))
	}
}

func countWidth(n int) int {
	if n <= 0 {
		return 1
	}
	w := 0
	for n > 0 {
		w++
		n /= 10
	}
	return w
}

func padRight(s string, width int) string {
	diff := width - runeLen(s)
	if diff <= 0 {
		return s
	}
	return s + strings.Repeat(" ", diff)
}

func padLeft(s string, width int) string {
	diff := width - runeLen(s)
	if diff <= 0 {
		return s
	}
	return strings.Repeat(" ", diff) + s
}

func formatKindCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	type kv struct {
		key   string
		count int
	}
	var sorted []kv
	for k, v := range counts {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].count != sorted[j].count {
			return sorted[i].count > sorted[j].count
		}
		return sorted[i].key < sorted[j].key
	})

	var parts []string
	for _, s := range sorted {
		parts = append(parts, fmt.Sprintf("%s: %d", s.key, s.count))
	}
	return strings.Join(parts, ", ")
}

func writeTimelineJSON(output io.Writer, blocks []apptypes.TimelineBlock) error {
	result := make([]timelineBlockOutput, 0, len(blocks))
	for _, b := range blocks {
		breakdown := b.WorkspaceBreakdown()
		wsOut := make([]timelineWorkspaceBreakdownOutput, 0, len(breakdown))
		for _, ws := range breakdown {
			wsOut = append(wsOut, timelineWorkspaceBreakdownOutput{
				Workspace:     ws.Workspace(),
				EventCount:    ws.EventCount(),
				KindCounts:    computeKindCounts(ws.Kinds()),
				Agents:        ws.Agents(),
				Summary:       ws.Summary(),
				SummarySource: string(ws.SummarySource()),
			})
		}
		result = append(result, timelineBlockOutput{
			Start:              formatJSONTime(b.BlockStart()),
			End:                formatJSONTime(b.BlockEnd()),
			DurationSec:        b.BlockEnd().Sub(b.BlockStart()).Seconds(),
			EventCount:         b.EventCount(),
			Workspaces:         b.Workspaces(),
			Agents:             b.Agents(),
			KindCounts:         computeKindCounts(b.Kinds()),
			WorkspaceBreakdown: wsOut,
		})
	}

	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to marshal timeline JSON: %w", err)
	}
	if _, err := fmt.Fprintln(output, string(encoded)); err != nil {
		return xerrors.Errorf("failed to print timeline JSON: %w", err)
	}
	return nil
}

func parseDateFlag(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", trimmed); err == nil {
		return t, nil
	}
	return time.Time{}, xerrors.Errorf("unsupported date format: %s (use YYYY-MM-DD or RFC3339)", trimmed)
}
