package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

func (c *RootCLI) newAuditCommand() *cobra.Command {
	var (
		dbPath         string
		client         string
		agent          string
		sessionID      string
		repo           string
		maxInputBytes  int
		maxOutputBytes int
	)

	auditCmd := &cobra.Command{
		Use:   "audit <command> <input> <output>",
		Short: "コマンド実行の監査ログを記録する",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runAudit(cmd.Context(), cmd.OutOrStdout(), auditCommandInput{
				dbPath:         dbPath,
				command:        args[0],
				input:          args[1],
				output:         args[2],
				client:         client,
				agent:          agent,
				sessionID:      sessionID,
				repo:           repo,
				maxInputBytes:  maxInputBytes,
				maxOutputBytes: maxOutputBytes,
			})
		},
	}
	auditCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	auditCmd.Flags().StringVar(&client, "client", "", "記録経路 (env: TRACEARY_CLIENT)")
	auditCmd.Flags().StringVar(&agent, "agent", "", "作業主体 (env: TRACEARY_AGENT)")
	auditCmd.Flags().StringVar(&sessionID, "session-id", "", "セッション ID (env: TRACEARY_SESSION_ID)")
	auditCmd.Flags().StringVar(&repo, "repo", "", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)")
	auditCmd.Flags().IntVar(
		&maxInputBytes,
		"max-input-bytes",
		0,
		"入力保存サイズ上限 (env: TRACEARY_MAX_AUDIT_INPUT_BYTES, 0 なら既定値)",
	)
	auditCmd.Flags().IntVar(
		&maxOutputBytes,
		"max-output-bytes",
		0,
		"出力保存サイズ上限 (env: TRACEARY_MAX_AUDIT_OUTPUT_BYTES, 0 なら既定値)",
	)

	return auditCmd
}

type auditCommandInput struct {
	dbPath         string
	command        string
	input          string
	output         string
	client         string
	agent          string
	sessionID      string
	repo           string
	maxInputBytes  int
	maxOutputBytes int
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

	maxInputBytes, err := resolveAuditMaxBytes(input.maxInputBytes, "TRACEARY_MAX_AUDIT_INPUT_BYTES")
	if err != nil {
		return xerrors.Errorf("input 上限の解決に失敗しました: %w", err)
	}
	maxOutputBytes, err := resolveAuditMaxBytes(input.maxOutputBytes, "TRACEARY_MAX_AUDIT_OUTPUT_BYTES")
	if err != nil {
		return xerrors.Errorf("output 上限の解決に失敗しました: %w", err)
	}

	event, commandAudit, err := c.recordCommandAuditUsecase.Run(ctx, usecase.RecordCommandAuditInput{
		DBPath:         resolvedPath,
		Command:        input.command,
		Input:          input.input,
		Output:         input.output,
		Client:         resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue),
		Agent:          resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue),
		SessionID:      resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", defaultSessionIDValue),
		Repo:           resolveRepoValue(ctx, input.repo),
		MaxInputBytes:  maxInputBytes,
		MaxOutputBytes: maxOutputBytes,
	})
	if err != nil {
		return xerrors.Errorf("監査ログ記録に失敗しました: %w", err)
	}

	if _, err := fmt.Fprintf(output, "記録しました: %s\n", event.EventID()); err != nil {
		return xerrors.Errorf("監査ログ記録結果の出力に失敗しました: %w", err)
	}
	if commandAudit != nil {
		if commandAudit.InputTruncated() {
			if _, err := fmt.Fprintln(output, "入力は切り詰めて保存しました"); err != nil {
				return xerrors.Errorf("input 切り詰め通知の出力に失敗しました: %w", err)
			}
		}
		if commandAudit.OutputTruncated() {
			if _, err := fmt.Fprintln(output, "出力は切り詰めて保存しました"); err != nil {
				return xerrors.Errorf("output 切り詰め通知の出力に失敗しました: %w", err)
			}
		}
	}

	return nil
}

func resolveAuditMaxBytes(flagValue int, envKey string) (int, error) {
	if flagValue != 0 {
		if flagValue < 0 {
			return 0, xerrors.Errorf("0 以上で指定してください")
		}
		return flagValue, nil
	}

	envValue, exists := os.LookupEnv(envKey)
	if !exists {
		return 0, nil
	}

	trimmedEnvValue := strings.TrimSpace(envValue)
	if trimmedEnvValue == "" {
		return 0, nil
	}

	parsedValue, err := strconv.Atoi(trimmedEnvValue)
	if err != nil {
		return 0, xerrors.Errorf("%s は整数で指定してください: %w", envKey, err)
	}
	if parsedValue < 0 {
		return 0, xerrors.Errorf("%s は 0 以上で指定してください", envKey)
	}

	return parsedValue, nil
}
