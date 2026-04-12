package cli

import (
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newSessionHandoffCommand() *cobra.Command {
	var (
		dbPath    string
		sessionID string
		repo      string
		recent    int
	)

	cmd := &cobra.Command{
		Use:   "handoff",
		Short: Localize("Print a concise session summary for handoff", "引き継ぎ用の簡潔なセッションサマリーを出力する"),
		Long: Localize(
			"Print a concise summary of the current session state, suitable for\nhandoff to another agent or context resumption after compact/clear.",
			"現在のセッション状態の簡潔なサマリーを出力します。\n別エージェントへの引き継ぎや compact/clear 後の再開に使えます。",
		),
		Aliases: []string{"context"},
		Args:    noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			output := cmd.OutOrStdout()

			resolvedDBPath, err := resolveDBPath(dbPath)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
			}
			if err := c.storeManagement.Initialize(ctx); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
			}

			resolvedRepo := resolveWorkspaceValue(ctx, repo)
			resolvedSessionID := resolveOptionalValue(sessionID, "TRACEARY_SESSION_ID", "")

			return c.printCompactSummary(ctx, output, resolvedDBPath, resolvedSessionID, resolvedRepo, recent)
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID", "セッション ID"))
	cmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().IntVar(&recent, "recent", 5, Localize("number of recent commands to show", "表示する直近コマンド数"))

	return cmd
}
