package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryDecayCommandInput struct {
	dbPath        string
	olderThan     time.Duration
	limit         int
	workspace     string
	includeHidden bool
	dedupe        bool
	apply         bool
	asJSON        bool
}

type memoryInboxRestoreCommandInput struct {
	dbPath       string
	ids          []string
	expiredAfter string
	limit        int
	asJSON       bool
}

func (c *RootCLI) newMemoryDecayCommand() *cobra.Command {
	input := memoryDecayCommandInput{
		olderThan: domtypes.DefaultMemoryDecayOlderThan,
		limit:     500,
	}
	cmd := &cobra.Command{
		Use:   "decay",
		Short: Localize("Expire stale auto-extracted memory candidates (dry-run by default)", "古い auto-extracted メモリ候補を expire する (既定は dry-run)"),
		Long: Localize(
			"Preview or apply non-destructive decay of unreviewed auto-extracted memory candidates to status=expired. Never auto-accepts. Use --apply to write. Pair with `memory inbox restore` for recovery.",
			"未レビューの auto-extracted メモリ候補を status=expired へ非破壊に decay します（auto-accept しません）。書き込みは --apply。復元は `memory inbox restore`。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryDecay(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().DurationVar(&input.olderThan, "older-than", domtypes.DefaultMemoryDecayOlderThan, Localize("only expire candidates not updated within this duration (default 720h)", "この duration 以上更新のない候補のみ expire (既定 720h)"))
	cmd.Flags().IntVar(&input.limit, "limit", 500, Localize("maximum candidates to expire in one run", "1 回の実行で expire する最大件数"))
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("filter by workspace scope", "workspace scope で絞り込む"))
	cmd.Flags().BoolVar(&input.includeHidden, "include-hidden", false, Localize("include extracted-hidden sources (already in default policy)", "extracted-hidden を含める (既定ポリシーに含まれる)"))
	cmd.Flags().BoolVar(&input.dedupe, "dedupe", false, Localize("also supersede exact duplicate candidates", "完全重複の candidate を supersede する"))
	cmd.Flags().BoolVar(&input.apply, "apply", false, Localize("apply decay writes; omit for dry-run preview", "decay を書き込む。省略時は dry-run preview"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemoryInboxRestoreCommand() *cobra.Command {
	input := memoryInboxRestoreCommandInput{limit: 500}
	cmd := &cobra.Command{
		Use:   "restore",
		Short: Localize("Restore expired memories back to candidates", "expired メモリを candidate に戻す"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryInboxRestore(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringSliceVar(&input.ids, "ids", nil, Localize("comma-separated memory ids to restore", "restore する memory id (カンマ区切り)"))
	cmd.Flags().StringVar(&input.expiredAfter, "expired-after", "", Localize("restore expired memories with expires_at at or after this RFC3339 time", "expires_at がこの RFC3339 時刻以降の expired を restore"))
	cmd.Flags().IntVar(&input.limit, "limit", 500, Localize("maximum rows when using --expired-after", "--expired-after 時の最大件数"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) runMemoryDecay(ctx context.Context, output io.Writer, input memoryDecayCommandInput) error {
	if c.memory == nil {
		return xerrors.New(Localize("memory usecase is not configured", "memory usecase が設定されていません"))
	}
	if c.storeManagement == nil {
		return xerrors.New(Localize("store management usecase is not configured", "store management usecase が設定されていません"))
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}

	criteria := apptypes.MemoryDecayCriteria{
		OlderThan:     input.olderThan,
		Limit:         input.limit,
		Apply:         input.apply,
		Dedupe:        input.dedupe,
		IncludeHidden: input.includeHidden,
	}
	if ws := strings.TrimSpace(input.workspace); ws != "" {
		criteria.Workspace = domtypes.Some(domtypes.Workspace(ws))
	}
	result, err := c.memory.Decay(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("memory decay failed", "memory decay に失敗しました"), err)
	}
	if input.asJSON {
		return writeJSON(output, result)
	}
	mode := "dry-run"
	if result.Applied {
		mode = "applied"
	}
	_, err = fmt.Fprintf(output, "memory decay (%s): scanned=%d expired=%d superseded=%d remaining=%d older_than=%s\n",
		mode, result.Scanned, len(result.ExpiredIDs), len(result.SupersededIDs), result.RemainingAfter, result.OlderThan)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory decay summary", "memory decay サマリの出力に失敗しました"), err)
	}
	if len(result.ExpiredIDs) > 0 && len(result.ExpiredIDs) <= 20 {
		for _, id := range result.ExpiredIDs {
			if _, err := fmt.Fprintf(output, "  expire %s\n", id); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print memory decay id", "memory decay id の出力に失敗しました"), err)
			}
		}
	}
	return nil
}

func (c *RootCLI) runMemoryInboxRestore(ctx context.Context, output io.Writer, input memoryInboxRestoreCommandInput) error {
	if c.memory == nil {
		return xerrors.New(Localize("memory usecase is not configured", "memory usecase が設定されていません"))
	}
	if c.storeManagement == nil {
		return xerrors.New(Localize("store management usecase is not configured", "store management usecase が設定されていません"))
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}

	ids := append([]string(nil), input.ids...)
	if after := strings.TrimSpace(input.expiredAfter); after != "" {
		t, err := time.Parse(time.RFC3339, after)
		if err != nil {
			return xerrors.Errorf("invalid --expired-after: %w", err)
		}
		// List expired and filter by expires_at client-side via List.
		summaries, err := c.memory.List(ctx, apptypes.NewMemoryListCriteriaBuilder(input.limit).
			Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusExpired}).
			Build())
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to list expired memories", "expired メモリの一覧取得に失敗しました"), err)
		}
		for _, s := range summaries {
			if exp, ok := s.ExpiresAt().Value(); ok && !exp.Before(t) {
				ids = append(ids, s.MemoryID().String())
			}
		}
	}
	if len(ids) == 0 {
		return xerrors.New(Localize("provide --ids or --expired-after matching expired memories", "--ids または該当する --expired-after を指定してください"))
	}

	restored := make([]string, 0, len(ids))
	for _, raw := range ids {
		mid, err := domtypes.MemoryIDFrom(strings.TrimSpace(raw))
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("invalid memory id", "不正な memory id です"), err)
		}
		if _, err := c.memory.Restore(ctx, mid); err != nil {
			return xerrors.Errorf("restore %s: %w", mid, err)
		}
		restored = append(restored, mid.String())
	}
	if input.asJSON {
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"restored": restored}); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode restore result", "restore 結果の JSON 出力に失敗しました"), err)
		}
		return nil
	}
	if _, err = fmt.Fprintf(output, "restored %d memory candidate(s)\n", len(restored)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print restore summary", "restore サマリの出力に失敗しました"), err)
	}
	return nil
}
