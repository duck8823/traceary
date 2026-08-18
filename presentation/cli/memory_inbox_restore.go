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

type memoryInboxRestoreCommandInput struct {
	dbPath       string
	ids          []string
	expiredAfter string
	limit        int
	asJSON       bool
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
