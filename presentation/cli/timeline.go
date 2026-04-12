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

const defaultGapMinutes = 15

func (c *RootCLI) newTimelineCommand() *cobra.Command {
	var (
		dbPath    string
		workspace string
		from      string
		to        string
		gap       int
		limit     int
		asJSON    bool
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
				to:        to,
				gap:       gap,
				limit:     limit,
				asJSON:    asJSON,
			})
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&workspace, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().StringVar(&from, "from", "", Localize("start date (YYYY-MM-DD or RFC3339)", "開始日 (YYYY-MM-DD または RFC3339)"))
	cmd.Flags().StringVar(&to, "to", "", Localize("end date (YYYY-MM-DD or RFC3339)", "終了日 (YYYY-MM-DD または RFC3339)"))
	cmd.Flags().IntVar(&gap, "gap", defaultGapMinutes, Localize("idle gap threshold in minutes", "アイドル判定の閾値（分）"))
	cmd.Flags().IntVar(&limit, "limit", 20, Localize("maximum number of blocks to display", "表示するブロック数の上限"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

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

	fromTime, err := parseDateFlag(input.from)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("invalid --from value", "--from の値が不正です"), err)
	}
	toTime, err := parseDateFlag(input.to)
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

	return writeTimelineText(output, blocks)
}

func computeKindCounts(kinds []string) map[string]int {
	counts := make(map[string]int)
	for _, k := range kinds {
		counts[k]++
	}
	return counts
}

func writeTimelineText(output io.Writer, blocks []apptypes.TimelineBlock) error {
	if len(blocks) == 0 {
		if _, err := fmt.Fprintln(output, "No work blocks found."); err != nil {
			return xerrors.Errorf("failed to print timeline: %w", err)
		}
		return nil
	}

	for _, b := range blocks {
		duration := b.BlockEnd().Sub(b.BlockStart())
		durationStr := formatDuration(duration)
		ws := strings.Join(b.Workspaces(), ", ")

		if _, err := fmt.Fprintf(output, "%s - %s (%s) %s\n",
			b.BlockStart().Local().Format("2006-01-02 15:04"),
			b.BlockEnd().Local().Format("15:04"),
			durationStr,
			ws,
		); err != nil {
			return xerrors.Errorf("failed to print timeline block: %w", err)
		}

		// Print kind counts as command frequency
		kindCounts := computeKindCounts(b.Kinds())
		kindLine := formatKindCounts(kindCounts)
		if kindLine != "" {
			if _, err := fmt.Fprintf(output, "  %s\n", kindLine); err != nil {
				return xerrors.Errorf("failed to print timeline block details: %w", err)
			}
		}
	}
	return nil
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
		return sorted[i].count > sorted[j].count
	})

	var parts []string
	for _, s := range sorted {
		parts = append(parts, fmt.Sprintf("%s: %d", s.key, s.count))
	}
	return strings.Join(parts, ", ")
}

func writeTimelineJSON(output io.Writer, blocks []apptypes.TimelineBlock) error {
	type jsonBlock struct {
		Start      string         `json:"start"`
		End        string         `json:"end"`
		Duration   string         `json:"duration"`
		EventCount int            `json:"event_count"`
		Workspaces []string       `json:"workspaces"`
		Agents     []string       `json:"agents"`
		KindCounts map[string]int `json:"kind_counts"`
	}

	result := make([]jsonBlock, 0, len(blocks))
	for _, b := range blocks {
		result = append(result, jsonBlock{
			Start:      b.BlockStart().Format(time.RFC3339),
			End:        b.BlockEnd().Format(time.RFC3339),
			Duration:   formatDuration(b.BlockEnd().Sub(b.BlockStart())),
			EventCount: b.EventCount(),
			Workspaces: b.Workspaces(),
			Agents:     b.Agents(),
			KindCounts: computeKindCounts(b.Kinds()),
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
