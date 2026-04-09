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

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation"
)

func (c *RootCLI) newAuditCommand() *cobra.Command {
	var (
		dbPath         string
		client         string
		agent          string
		sessionID      string
		repo           string
		command        string
		commandFlagSet bool
		auditInput     string
		inputFlagSet   bool
		auditOutput    string
		outputFlagSet  bool
		idOnly         bool
		asJSON         bool
		allowSecrets   bool
		maxInputBytes  int
		maxOutputBytes int
	)

	auditCmd := &cobra.Command{
		Use:   "audit <command> [<input>] [<output>]",
		Short: Localize("Record a command execution audit event", "コマンド実行の監査ログを記録する"),
		Long: Localize(
			"Record a shell-command audit.\n\nDefaults:\n- DB path: --db-path -> TRACEARY_DB_PATH -> ~/.config/traceary/traceary.db\n- client / agent / repo: flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_REPO -> cli / manual / detected repo\n- session ID: --session-id -> TRACEARY_SESSION_ID -> latest non-stale active session for the resolved repo -> default\n- input / output: optional; omit them when you only need the command text\n- secret policy: --allow-secrets or TRACEARY_ALLOW_SECRETS disables best-effort redaction",
			"shell command の監査ログを記録します。\n\n既定値の解決順:\n- DB path: --db-path -> TRACEARY_DB_PATH -> ~/.config/traceary/traceary.db\n- client / agent / repo: flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_REPO -> cli / manual / 検出した repo\n- session ID: --session-id -> TRACEARY_SESSION_ID -> 解決した repo の最新 non-stale active session -> default\n- input / output: 任意。command 文字列だけを記録したい場合は省略できます\n- secret policy: --allow-secrets または TRACEARY_ALLOW_SECRETS で best-effort redaction を無効化します",
		),
		Example: strings.Join([]string{
			"  traceary audit \"go test ./...\"",
			"  traceary audit \"go test ./...\" '{}' '{\"exitCode\":0}'",
			"  traceary audit --command \"npm test\" --input '{}' --output '{\"exitCode\":1}' --json",
		}, "\n"),
		Args: maximumNArgsLocalized(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			commandValue, inputValue, outputValue, err := resolveAuditPayload(auditPayloadInput{
				positionalArgs: args,
				command:        command,
				commandFlagSet: commandFlagSet,
				input:          auditInput,
				inputFlagSet:   inputFlagSet,
				output:         auditOutput,
				outputFlagSet:  outputFlagSet,
			})
			if err != nil {
				return err
			}

			return c.runAudit(cmd.Context(), cmd.OutOrStdout(), auditCommandInput{
				dbPath:         dbPath,
				command:        commandValue,
				input:          inputValue,
				output:         outputValue,
				client:         client,
				agent:          agent,
				sessionID:      sessionID,
				repo:           repo,
				idOnly:         idOnly,
				asJSON:         asJSON,
				allowSecrets:   allowSecrets,
				maxInputBytes:  maxInputBytes,
				maxOutputBytes: maxOutputBytes,
			})
		},
	}
	auditCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	auditCmd.Flags().StringVar(&client, "client", "", Localize("recording channel (env: TRACEARY_CLIENT)", "記録経路 (env: TRACEARY_CLIENT)"))
	auditCmd.Flags().StringVar(&agent, "agent", "", Localize("actor name (env: TRACEARY_AGENT)", "作業主体 (env: TRACEARY_AGENT)"))
	auditCmd.Flags().StringVar(
		&sessionID,
		"session-id",
		"",
		Localize(
			"session ID (env: TRACEARY_SESSION_ID, otherwise active session or default)",
			"セッション ID (env: TRACEARY_SESSION_ID。未指定時は active session、なければ既定値)",
		),
	)
	auditCmd.Flags().StringVar(&repo, "repo", "", Localize("auxiliary work context identifier (env: TRACEARY_REPO)", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)"))
	auditCmd.Flags().StringVar(&command, "command", "", Localize("command text to record", "記録する command 文字列"))
	auditCmd.Flags().StringVar(&auditInput, "input", "", Localize("optional command input payload", "任意の command input"))
	auditCmd.Flags().StringVar(&auditOutput, "output", "", Localize("optional command output payload", "任意の command output"))
	auditCmd.Flags().BoolVar(&idOnly, "id-only", false, Localize("print only the recorded event ID", "記録した event ID だけを出力する"))
	auditCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
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
	auditCmd.PreRun = func(cmd *cobra.Command, _ []string) {
		commandFlagSet = cmd.Flags().Changed("command")
		inputFlagSet = cmd.Flags().Changed("input")
		outputFlagSet = cmd.Flags().Changed("output")
	}
	auditCmd.MarkFlagsMutuallyExclusive("id-only", "json")

	return auditCmd
}

type auditPayloadInput struct {
	positionalArgs []string
	command        string
	commandFlagSet bool
	input          string
	inputFlagSet   bool
	output         string
	outputFlagSet  bool
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
	idOnly         bool
	asJSON         bool
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

	resolvedRepo := resolveRepoValue(ctx, input.repo)
	sessionResolution, err := c.resolveManualSessionID(
		ctx,
		resolvedPath,
		resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", ""),
		resolvedRepo,
	)
	if err != nil {
		return err
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

	config := presentation.LoadConfig()

	event, commandAudit, err := c.recordCommandAuditUsecase.Run(ctx, usecase.RecordCommandAuditInput{
		DBPath:              resolvedPath,
		Command:             input.command,
		Input:               input.input,
		Output:              input.output,
		Client:              resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue),
		Agent:               resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue),
		SessionID:           sessionResolution.sessionID,
		Repo:                resolvedRepo,
		AllowSecrets:        allowSecrets,
		MaxInputBytes:       maxInputBytes,
		MaxOutputBytes:      maxOutputBytes,
		ExtraRedactPatterns: config.Redact.ExtraPatterns,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record command audit", "監査ログ記録に失敗しました"), err)
	}
	if input.asJSON {
		eventDetails, err := queryservice.NewEventDetails(event, commandAudit)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to build audit result", "監査ログ結果の構築に失敗しました"), err)
		}
		if err := writeEventDetailsJSON(output, eventDetails); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print record result", "監査ログ記録結果の出力に失敗しました"), err)
		}
		return nil
	}

	if input.idOnly {
		if _, err := fmt.Fprintln(output, event.EventID()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print record result", "監査ログ記録結果の出力に失敗しました"), err)
		}
		return nil
	}

	if err := writeManualSessionNotice(output, sessionResolution.notice); err != nil {
		return err
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

func resolveAuditPayload(input auditPayloadInput) (string, string, string, error) {
	commandValue, err := resolveAuditPayloadField(
		input.command,
		input.commandFlagSet,
		input.positionalArgs,
		0,
		Localize("--command", "--command"),
		true,
	)
	if err != nil {
		return "", "", "", err
	}
	inputValue, err := resolveAuditPayloadField(
		input.input,
		input.inputFlagSet,
		input.positionalArgs,
		1,
		Localize("--input", "--input"),
		false,
	)
	if err != nil {
		return "", "", "", err
	}
	outputValue, err := resolveAuditPayloadField(
		input.output,
		input.outputFlagSet,
		input.positionalArgs,
		2,
		Localize("--output", "--output"),
		false,
	)
	if err != nil {
		return "", "", "", err
	}

	return commandValue, inputValue, outputValue, nil
}

func resolveAuditPayloadField(
	flagValue string,
	flagSet bool,
	positionalArgs []string,
	position int,
	flagName string,
	requireNonEmpty bool,
) (string, error) {
	hasPositionalValue := len(positionalArgs) > position
	if flagSet && hasPositionalValue {
		return "", xerrors.Errorf(
			localizef("do not provide both %s and positional argument %d", "%s と位置引数 %d を同時に指定しないでください", flagName, position+1),
		)
	}

	if flagSet {
		if requireNonEmpty && strings.TrimSpace(flagValue) == "" {
			return "", xerrors.Errorf(localizef("%s must not be empty", "%s は空にできません", flagName))
		}
		return flagValue, nil
	}
	if hasPositionalValue {
		if requireNonEmpty && strings.TrimSpace(positionalArgs[position]) == "" {
			return "", xerrors.Errorf(localizef("positional argument %d must not be empty", "位置引数 %d は空にできません", position+1))
		}
		return positionalArgs[position], nil
	}
	if requireNonEmpty {
		return "", xerrors.Errorf(localizef("either %s or positional argument %d is required", "%s または位置引数 %d が必要です", flagName, position+1))
	}
	return "", nil
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
