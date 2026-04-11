package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

func (c *RootCLI) newSessionLabelCommand() *cobra.Command {
	var (
		dbPath    string
		sessionID string
	)

	labelCmd := &cobra.Command{
		Use:   "label [label-text]",
		Short: Localize("Set a label on a session", "セッションにラベルを設定する"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			output := cmd.OutOrStdout()

			_, err := resolveDBPath(dbPath)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
			}

			if err := c.initializeStoreUsecase.Run(ctx); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
			}

			resolvedSessionID := resolveOptionalValue(sessionID, "TRACEARY_SESSION_ID", "")
			if resolvedSessionID == "" {
				return xerrors.Errorf("%s", Localize("--session-id is required", "--session-id は必須です"))
			}

			if err := c.updateSessionLabelUsecase.Run(ctx, usecase.UpdateSessionLabelInput{
				SessionID: resolvedSessionID,
				Label:     args[0],
			}); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to update session label", "セッションラベルの更新に失敗しました"), err)
			}

			return printSessionLabelResult(output, resolvedSessionID, args[0])
		},
	}

	labelCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	labelCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID to label", "ラベルを設定するセッション ID"))

	return labelCmd
}

func printSessionLabelResult(output io.Writer, sessionID string, label string) error {
	if _, err := fmt.Fprintf(output, "%s: %s -> %s\n",
		Localize("Label set", "ラベルを設定しました"),
		sessionID,
		label,
	); err != nil {
		return xerrors.Errorf("failed to print result: %w", err)
	}
	return nil
}
