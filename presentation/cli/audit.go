package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

func (c *RootCLI) newAuditCommand() *cobra.Command {
	var (
		dbPath    string
		client    string
		agent     string
		sessionID string
		repo      string
	)

	auditCmd := &cobra.Command{
		Use:   "audit <command> <input> <output>",
		Short: "コマンド実行の監査ログを記録する",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runAudit(cmd.Context(), cmd.OutOrStdout(), auditCommandInput{
				dbPath:    dbPath,
				command:   args[0],
				input:     args[1],
				output:    args[2],
				client:    client,
				agent:     agent,
				sessionID: sessionID,
				repo:      repo,
			})
		},
	}
	auditCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	auditCmd.Flags().StringVar(&client, "client", "", "記録経路 (env: TRACEARY_CLIENT)")
	auditCmd.Flags().StringVar(&agent, "agent", "", "作業主体 (env: TRACEARY_AGENT)")
	auditCmd.Flags().StringVar(&sessionID, "session-id", "", "セッション ID (env: TRACEARY_SESSION_ID)")
	auditCmd.Flags().StringVar(&repo, "repo", "", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)")

	return auditCmd
}

type auditCommandInput struct {
	dbPath    string
	command   string
	input     string
	output    string
	client    string
	agent     string
	sessionID string
	repo      string
}

func (c *RootCLI) runAudit(ctx context.Context, output io.Writer, input auditCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.recordCommandAuditUsecase == nil {
		return xerrors.Errorf("監査ログ記録ユースケースが設定されていません")
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	event, _, err := c.recordCommandAuditUsecase.Run(ctx, usecase.RecordCommandAuditInput{
		DBPath:    resolvedPath,
		Command:   input.command,
		Input:     input.input,
		Output:    input.output,
		Client:    resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue),
		Agent:     resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue),
		SessionID: resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", defaultSessionIDValue),
		Repo:      resolveRepoValue(ctx, input.repo),
	})
	if err != nil {
		return xerrors.Errorf("監査ログ記録に失敗しました: %w", err)
	}

	if _, err := fmt.Fprintf(output, "記録しました: %s\n", event.EventID()); err != nil {
		return xerrors.Errorf("監査ログ記録結果の出力に失敗しました: %w", err)
	}

	return nil
}
