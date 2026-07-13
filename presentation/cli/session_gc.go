package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionGCCommand() *cobra.Command {
	var (
		dbPath     string
		staleAfter time.Duration
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "gc",
		Short: Localize("Close stale sessions", "stale なセッションを終了する"),
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

			if staleAfter <= 0 {
				return xerrors.Errorf("--stale-after must be greater than 0")
			}

			result, err := c.storeManagement.CloseStaleSessions(ctx, staleAfter, dryRun, types.SessionID(""))
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to close stale sessions", "stale セッションの終了に失敗しました"), err)
			}

			if dryRun {
				if _, err := fmt.Fprintf(output, "%s: %d\n",
					Localize("stale sessions found (dry-run)", "stale セッションが見つかりました (dry-run)"),
					result.ClosedCount(),
				); err != nil {
					return xerrors.Errorf("failed to print result: %w", err)
				}
			} else {
				if _, err := fmt.Fprintf(output, "%s: %d\n",
					Localize("stale sessions closed", "stale セッションを終了しました"),
					result.ClosedCount(),
				); err != nil {
					return xerrors.Errorf("failed to print result: %w", err)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().DurationVar(&staleAfter, "stale-after", 24*time.Hour, Localize("close sessions with no activity for this duration", "この期間活動のないセッションを終了する"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, Localize("print stale sessions without closing", "終了せずに stale セッションを表示する"))

	return cmd
}
