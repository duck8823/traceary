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
		allowSecrets   bool
		maxInputBytes  int
		maxOutputBytes int
	)

	auditCmd := &cobra.Command{
		Use:   "audit <command> <input> <output>",
		Short: Localize("Record a command execution audit event", "コマンド実行の監査ログを記録する"),
		Args:  exactArgsJP(3),
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
				allowSecrets:   allowSecrets,
				maxInputBytes:  maxInputBytes,
				maxOutputBytes: maxOutputBytes,
			})
		},
	}
	auditCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	auditCmd.Flags().StringVar(&client, "client", "", Localize("recording channel (env: TRACEARY_CLIENT)", "記録経路 (env: TRACEARY_CLIENT)"))
	auditCmd.Flags().StringVar(&agent, "agent", "", Localize("actor name (env: TRACEARY_AGENT)", "作業主体 (env: TRACEARY_AGENT)"))
	auditCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID (env: TRACEARY_SESSION_ID)", "セッション ID (env: TRACEARY_SESSION_ID)"))
	auditCmd.Flags().StringVar(&repo, "repo", "", Localize("auxiliary work context identifier (env: TRACEARY_REPO)", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)"))
	auditCmd.Flags().BoolVar(
		&allowSecrets,
		"allow-secrets",
		false,
		Localize("store input/output without the default secret redaction (env: TRACEARY_ALLOW_SECRETS)", "既定の secret redaction を行わずに input/output を保存する (env: TRACEARY_ALLOW_SECRETS)"),
	)
	auditCmd.Flags().IntVar(
		&maxInputBytes,
		"max-input-bytes",
		0,
		Localize("maximum stored input bytes (env: TRACEARY_MAX_AUDIT_INPUT_BYTES, 0 uses default)", "入力保存サイズ上限 (env: TRACEARY_MAX_AUDIT_INPUT_BYTES, 0 なら既定値)"),
	)
	auditCmd.Flags().IntVar(
		&maxOutputBytes,
		"max-output-bytes",
		0,
		Localize("maximum stored output bytes (env: TRACEARY_MAX_AUDIT_OUTPUT_BYTES, 0 uses default)", "出力保存サイズ上限 (env: TRACEARY_MAX_AUDIT_OUTPUT_BYTES, 0 なら既定値)"),
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
	allowSecrets   bool
	maxInputBytes  int
	maxOutputBytes int
}

func (c *RootCLI) runAudit(ctx context.Context, output io.Writer, input auditCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.recordCommandAuditUsecase == nil {
		return xerrors.Errorf(Localize("record command audit usecase is not configured", "監査ログ記録ユースケースが設定されていません"))
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	maxInputBytes, err := resolveAuditMaxBytes(input.maxInputBytes, "TRACEARY_MAX_AUDIT_INPUT_BYTES")
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve input byte limit", "input 上限の解決に失敗しました"), err)
	}
	maxOutputBytes, err := resolveAuditMaxBytes(input.maxOutputBytes, "TRACEARY_MAX_AUDIT_OUTPUT_BYTES")
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve output byte limit", "output 上限の解決に失敗しました"), err)
	}
	allowSecrets, err := resolveAuditAllowSecrets(input.allowSecrets)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve secret handling policy", "secret 取り扱いポリシーの解決に失敗しました"), err)
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
		AllowSecrets:   allowSecrets,
		MaxInputBytes:  maxInputBytes,
		MaxOutputBytes: maxOutputBytes,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record command audit", "監査ログ記録に失敗しました"), err)
	}

	if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Recorded", "記録しました"), event.EventID()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print record result", "監査ログ記録結果の出力に失敗しました"), err)
	}
	if commandAudit != nil {
		if commandAudit.InputRedacted() {
			if _, err := fmt.Fprintln(output, Localize("Input was redacted before storing", "入力は伏せ字化して保存しました")); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print input redaction notice", "input 伏せ字化通知の出力に失敗しました"), err)
			}
		}
		if commandAudit.OutputRedacted() {
			if _, err := fmt.Fprintln(output, Localize("Output was redacted before storing", "出力は伏せ字化して保存しました")); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print output redaction notice", "output 伏せ字化通知の出力に失敗しました"), err)
			}
		}
		if commandAudit.InputTruncated() {
			if _, err := fmt.Fprintln(output, Localize("Input was truncated before storing", "入力は切り詰めて保存しました")); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print input truncation notice", "input 切り詰め通知の出力に失敗しました"), err)
			}
		}
		if commandAudit.OutputTruncated() {
			if _, err := fmt.Fprintln(output, Localize("Output was truncated before storing", "出力は切り詰めて保存しました")); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print output truncation notice", "output 切り詰め通知の出力に失敗しました"), err)
			}
		}
	}

	return nil
}

func resolveAuditMaxBytes(flagValue int, envKey string) (int, error) {
	if flagValue != 0 {
		if flagValue < 0 {
			return 0, xerrors.Errorf(Localize("value must be greater than or equal to 0", "0 以上で指定してください"))
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
		return 0, xerrors.Errorf("%s: %w", localizef("%s must be an integer", "%s は整数で指定してください", envKey), err)
	}
	if parsedValue < 0 {
		return 0, xerrors.Errorf(localizef("%s must be greater than or equal to 0", "%s は 0 以上で指定してください", envKey))
	}

	return parsedValue, nil
}

func resolveAuditAllowSecrets(flagValue bool) (bool, error) {
	if flagValue {
		return true, nil
	}

	envValue, exists := os.LookupEnv("TRACEARY_ALLOW_SECRETS")
	if !exists {
		return false, nil
	}

	trimmedEnvValue := strings.TrimSpace(envValue)
	if trimmedEnvValue == "" {
		return false, nil
	}

	parsedValue, err := strconv.ParseBool(trimmedEnvValue)
	if err != nil {
		return false, xerrors.Errorf("%s: %w", Localize("TRACEARY_ALLOW_SECRETS must be a boolean", "TRACEARY_ALLOW_SECRETS は boolean で指定してください"), err)
	}

	return parsedValue, nil
}
