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
		Short: Localize("Append a session note", "セッションログを追記する"),
		Args:  exactArgsJP(1),
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
	logCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	logCmd.Flags().StringVar(&client, "client", "", Localize("recording channel (env: TRACEARY_CLIENT)", "記録経路 (env: TRACEARY_CLIENT)"))
	logCmd.Flags().StringVar(&agent, "agent", "", Localize("actor name (env: TRACEARY_AGENT)", "作業主体 (env: TRACEARY_AGENT)"))
	logCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID (env: TRACEARY_SESSION_ID)", "セッション ID (env: TRACEARY_SESSION_ID)"))
	logCmd.Flags().StringVar(&repo, "repo", "", Localize("auxiliary work context identifier (env: TRACEARY_REPO)", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)"))

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
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.recordLogUsecase == nil {
		return xerrors.Errorf(Localize("record log usecase is not configured", "ログ記録ユースケースが設定されていません"))
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	event, err := c.recordLogUsecase.Run(ctx, usecase.RecordLogInput{
		DBPath:    resolvedPath,
		Message:   input.message,
		Client:    resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue),
		Agent:     resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue),
		SessionID: resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", defaultSessionIDValue),
		Repo:      resolveRepoValue(ctx, input.repo),
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record log", "ログ記録に失敗しました"), err)
	}

	if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Recorded", "記録しました"), event.EventID()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print record result", "ログ記録結果の出力に失敗しました"), err)
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
