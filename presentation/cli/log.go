package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

const (
	defaultAgentValue     = "manual"
	defaultSessionIDValue = "default"
	defaultClientValue    = "cli"
)

func (c *RootCLI) newLogCommand() *cobra.Command {
	var (
		dbPath    string
		kind      string
		client    string
		agent     string
		sessionID string
		repo      string
		idOnly    bool
		asJSON    bool
	)

	logCmd := &cobra.Command{
		Use:   "log <message>",
		Short: Localize("Append a session note", "セッションログを追記する"),
		Long: Localize(
			"Append a note to Traceary.\n\nDefaults:\n- DB path: --db-path -> TRACEARY_DB_PATH -> ~/.config/traceary/traceary.db\n- client / agent / workspace: flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_WORKSPACE -> cli / manual / detected workspace\n- session ID: --session-id -> TRACEARY_SESSION_ID -> latest non-stale active session for the resolved workspace -> default",
			"Traceary にメモを追記します。\n\n既定値の解決順:\n- DB path: --db-path -> TRACEARY_DB_PATH -> ~/.config/traceary/traceary.db\n- client / agent / workspace: flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_WORKSPACE -> cli / manual / 検出した workspace\n- session ID: --session-id -> TRACEARY_SESSION_ID -> 解決した workspace の最新 non-stale active session -> default",
		),
		Example: strings.Join([]string{
			"  traceary log \"investigate retry behavior\"",
			"  traceary log --session-id session-123 --json \"checkpoint\"",
		}, "\n"),
		Args: exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runLog(cmd.Context(), cmd.OutOrStdout(), logCommandInput{
				dbPath:    dbPath,
				message:   args[0],
				kind:      kind,
				client:    client,
				agent:     agent,
				sessionID: sessionID,
				repo:      repo,
				idOnly:    idOnly,
				asJSON:    asJSON,
			})
		},
	}
	logCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	logCmd.Flags().StringVar(&kind, "kind", "", Localize("event kind (default: note)", "イベント種別 (既定: note)"))
	logCmd.Flags().StringVar(&client, "client", "", Localize("recording channel (env: TRACEARY_CLIENT)", "記録経路 (env: TRACEARY_CLIENT)"))
	logCmd.Flags().StringVar(&agent, "agent", "", Localize("actor name (env: TRACEARY_AGENT)", "作業主体 (env: TRACEARY_AGENT)"))
	logCmd.Flags().StringVar(
		&sessionID,
		"session-id",
		"",
		Localize(
			"session ID (env: TRACEARY_SESSION_ID, otherwise active session or default)",
			"セッション ID (env: TRACEARY_SESSION_ID。未指定時は active session、なければ既定値)",
		),
	)
	logCmd.Flags().StringVar(&repo, "workspace", "", Localize("auxiliary workspace identifier (env: TRACEARY_WORKSPACE)", "補助的な workspace 識別子 (env: TRACEARY_WORKSPACE)"))
	logCmd.Flags().BoolVar(&idOnly, "id-only", false, Localize("print only the recorded event ID", "記録した event ID だけを出力する"))
	logCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	logCmd.MarkFlagsMutuallyExclusive("id-only", "json")

	return logCmd
}

func (c *RootCLI) runLog(ctx context.Context, output io.Writer, input logCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.Errorf(Localize("record log usecase is not configured", "ログ記録ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	resolvedRepo := resolveWorkspaceValue(ctx, input.repo)
	sessionResolution, err := c.resolveManualSessionID(
		ctx,
		resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", ""),
		resolvedRepo,
	)
	if err != nil {
		return err
	}

	client, _ := types.ClientOf(resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue))
	agent, _ := types.AgentOf(resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue))
	sessionID, _ := types.SessionIDOf(sessionResolution.sessionID)
	workspace := types.Workspace(resolvedRepo)
	kind := types.EventKind(strings.TrimSpace(input.kind))
	event, err := c.event.Log(ctx, input.message, kind, client, agent, sessionID, workspace)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record log", "ログ記録に失敗しました"), err)
	}
	if input.asJSON {
		if err := writeEventJSON(output, event); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print record result", "ログ記録結果の出力に失敗しました"), err)
		}
		return nil
	}

	if input.idOnly {
		if _, err := fmt.Fprintln(output, event.EventID()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print record result", "ログ記録結果の出力に失敗しました"), err)
		}
		return nil
	}

	if err := writeManualSessionNotice(output, sessionResolution.notice); err != nil {
		return err
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
