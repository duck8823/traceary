package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

const (
	defaultAgentValue     = "manual"
	defaultSessionIDValue = "default"
	defaultClientValue    = "cli"
)

func (c *RootCLI) newLogCommand() *cobra.Command {
	var (
		dbPath    string
		client    string
		agent     string
		sessionID string
		repo      string
	)

	logCmd := &cobra.Command{
		Use:   "log <message>",
		Short: "セッションログを追記する",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runLog(cmd.Context(), cmd.OutOrStdout(), logCommandInput{
				dbPath:    dbPath,
				message:   args[0],
				client:    client,
				agent:     agent,
				sessionID: sessionID,
				repo:      repo,
			})
		},
	}
	logCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	logCmd.Flags().StringVar(&client, "client", "", "記録経路 (env: TRACEARY_CLIENT)")
	logCmd.Flags().StringVar(&agent, "agent", "", "作業主体 (env: TRACEARY_AGENT)")
	logCmd.Flags().StringVar(&sessionID, "session-id", "", "セッション ID (env: TRACEARY_SESSION_ID)")
	logCmd.Flags().StringVar(&repo, "repo", "", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)")

	return logCmd
}

type logCommandInput struct {
	dbPath    string
	message   string
	client    string
	agent     string
	sessionID string
	repo      string
}

func (c *RootCLI) runLog(ctx context.Context, output io.Writer, input logCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.recordLogUsecase == nil {
		return xerrors.Errorf("ログ記録ユースケースが設定されていません")
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	event, err := c.recordLogUsecase.Run(ctx, usecase.RecordLogInput{
		DBPath:    resolvedPath,
		Message:   input.message,
		Client:    resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue),
		Agent:     resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue),
		SessionID: resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", defaultSessionIDValue),
		Repo:      resolveOptionalValue(input.repo, "TRACEARY_REPO", ""),
	})
	if err != nil {
		return xerrors.Errorf("ログ記録に失敗しました: %w", err)
	}

	if _, err := fmt.Fprintf(output, "記録しました: %s\n", event.EventID()); err != nil {
		return xerrors.Errorf("ログ記録結果の出力に失敗しました: %w", err)
	}

	return nil
}

func resolveOptionalValue(flagValue string, envKey string, defaultValue string) string {
	trimmedFlagValue := strings.TrimSpace(flagValue)
	if trimmedFlagValue != "" {
		return trimmedFlagValue
	}

	if envValue, exists := os.LookupEnv(envKey); exists {
		trimmedEnvValue := strings.TrimSpace(envValue)
		if trimmedEnvValue != "" {
			return trimmedEnvValue
		}
	}

	return defaultValue
}
