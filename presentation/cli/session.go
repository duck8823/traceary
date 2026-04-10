package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const defaultActiveSessionStaleAfter = 24 * time.Hour

func (c *RootCLI) newSessionCommand() *cobra.Command {
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: Localize("Record session lifecycle events", "セッション境界を記録する"),
	}
	sessionCmd.AddCommand(c.newSessionStartCommand())
	sessionCmd.AddCommand(c.newSessionEndCommand())
	sessionCmd.AddCommand(c.newSessionLatestCommand())
	sessionCmd.AddCommand(c.newSessionActiveCommand())
	sessionCmd.AddCommand(c.newSessionListCommand())
	sessionCmd.AddCommand(c.newSessionLabelCommand())
	sessionCmd.AddCommand(c.newSessionHandoffCommand())
	sessionCmd.AddCommand(c.newSessionTreeCommand())

	return sessionCmd
}

func (c *RootCLI) newSessionLatestCommand() *cobra.Command {
	var (
		dbPath string
		client string
		agent  string
		repo   string
		asJSON bool
	)

	latestCmd := &cobra.Command{
		Use:   "latest",
		Short: Localize("Print the latest session ID", "直近のセッション ID を表示する"),
		Long: Localize(
			"Print the latest matching session ID.\n\n\"latest\" means the session whose most recent lifecycle boundary (start or end) is newest among the matches.\nFilters resolve as flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_REPO, and --repo falls back to the detected work context when omitted.",
			"条件に一致する直近の session ID を表示します。\n\nここでの「直近」は、一致した session のうち最新の lifecycle boundary (start または end) が最も新しいものを意味します。\nfilter は flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_REPO の順に解決し、--repo 省略時は検出した work context を使います。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionLatest(cmd.Context(), cmd.OutOrStdout(), sessionLatestCommandInput{
				dbPath: dbPath,
				client: client,
				agent:  agent,
				repo:   repo,
				asJSON: asJSON,
			})
		},
	}
	latestCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	latestCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "記録経路で絞り込む"))
	latestCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "作業主体で絞り込む"))
	latestCmd.Flags().StringVar(&repo, "repo", "", Localize("filter by auxiliary work context identifier", "補助的なコンテキスト識別子で絞り込む"))
	latestCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return latestCmd
}

func (c *RootCLI) newSessionStartCommand() *cobra.Command {
	var (
		dbPath          string
		client          string
		agent           string
		sessionID       string
		repo            string
		parentSessionID string
		idOnly          bool
		asJSON          bool
	)

	startCmd := &cobra.Command{
		Use:   "start",
		Short: Localize("Record session start", "セッション開始を記録する"),
		Long: Localize(
			"Record a session-start boundary.\n\nDefaults:\n- DB path: --db-path -> TRACEARY_DB_PATH -> ~/.config/traceary/traceary.db\n- client / agent / repo: flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_REPO -> cli / manual / detected repo\n- session ID: generate a new ID when --session-id is omitted",
			"session 開始境界を記録します。\n\n既定値の解決順:\n- DB path: --db-path -> TRACEARY_DB_PATH -> ~/.config/traceary/traceary.db\n- client / agent / repo: flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_REPO -> cli / manual / 検出した repo\n- session ID: --session-id を省略した場合は新しく採番します",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionBoundary(cmd.Context(), cmd.OutOrStdout(), sessionBoundaryCommandInput{
				dbPath:          dbPath,
				client:          client,
				agent:           agent,
				sessionID:       strings.TrimSpace(sessionID),
				repo:            repo,
				parentSessionID: strings.TrimSpace(parentSessionID),
				kind:            types.EventKindSessionStarted,
				idOnly:          idOnly,
				asJSON:          asJSON,
			})
		},
	}
	startCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	startCmd.Flags().StringVar(&client, "client", "", Localize("recording channel (env: TRACEARY_CLIENT)", "記録経路 (env: TRACEARY_CLIENT)"))
	startCmd.Flags().StringVar(&agent, "agent", "", Localize("actor name (env: TRACEARY_AGENT)", "作業主体 (env: TRACEARY_AGENT)"))
	startCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID to start", "開始するセッション ID"))
	startCmd.Flags().StringVar(&repo, "repo", "", Localize("auxiliary work context identifier (env: TRACEARY_REPO)", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)"))
	startCmd.Flags().StringVar(&parentSessionID, "parent-session-id", "", Localize("parent session ID for sub-agent sessions", "サブエージェントセッションの親セッション ID"))
	startCmd.Flags().BoolVar(&idOnly, "id-only", false, Localize("print only the resulting identifier", "結果の識別子だけを出力する"))
	startCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	startCmd.MarkFlagsMutuallyExclusive("id-only", "json")

	return startCmd
}

func (c *RootCLI) newSessionEndCommand() *cobra.Command {
	var (
		dbPath    string
		client    string
		agent     string
		sessionID string
		repo      string
		summary   string
		idOnly    bool
		asJSON    bool
	)

	endCmd := &cobra.Command{
		Use:   "end",
		Short: Localize("Record session end", "セッション終了を記録する"),
		Long: Localize(
			"Record a session-end boundary.\n\nDefaults:\n- session ID: --session-id -> TRACEARY_SESSION_ID\n- client / agent / repo: use explicit flags first, then backfill from the matching session start when possible",
			"session 終了境界を記録します。\n\n既定値の解決順:\n- session ID: --session-id -> TRACEARY_SESSION_ID\n- client / agent / repo: 明示 flag を優先し、足りない値は対応する session start から補完します",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionBoundary(cmd.Context(), cmd.OutOrStdout(), sessionBoundaryCommandInput{
				dbPath:    dbPath,
				client:    client,
				agent:     agent,
				sessionID: resolveOptionalValue(sessionID, "TRACEARY_SESSION_ID", ""),
				repo:      repo,
				summary:   summary,
				kind:      types.EventKindSessionEnded,
				idOnly:    idOnly,
				asJSON:    asJSON,
			})
		},
	}
	endCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	endCmd.Flags().StringVar(&client, "client", "", Localize("recording channel (env: TRACEARY_CLIENT)", "記録経路 (env: TRACEARY_CLIENT)"))
	endCmd.Flags().StringVar(&agent, "agent", "", Localize("actor name (env: TRACEARY_AGENT)", "作業主体 (env: TRACEARY_AGENT)"))
	endCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID to end (env: TRACEARY_SESSION_ID)", "終了するセッション ID (env: TRACEARY_SESSION_ID)"))
	endCmd.Flags().StringVar(&repo, "repo", "", Localize("auxiliary work context identifier (env: TRACEARY_REPO)", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)"))
	endCmd.Flags().StringVar(&summary, "summary", "", Localize("session summary text", "セッションサマリーテキスト"))
	endCmd.Flags().BoolVar(&idOnly, "id-only", false, Localize("print only the resulting identifier", "結果の識別子だけを出力する"))
	endCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	endCmd.MarkFlagsMutuallyExclusive("id-only", "json")

	return endCmd
}

type sessionBoundaryCommandInput struct {
	dbPath          string
	client          string
	agent           string
	sessionID       string
	repo            string
	summary         string
	parentSessionID string
	kind            types.EventKind
	idOnly          bool
	asJSON          bool
}

type sessionLatestCommandInput struct {
	dbPath     string
	client     string
	agent      string
	repo       string
	activeOnly bool
	staleAfter time.Duration
	allowStale bool
	asJSON     bool
}

func (c *RootCLI) newSessionActiveCommand() *cobra.Command {
	var (
		dbPath     string
		client     string
		agent      string
		repo       string
		staleAfter time.Duration
		allowStale bool
		asJSON     bool
	)

	activeCmd := &cobra.Command{
		Use:   "active",
		Short: Localize("Print the active session ID (stale sessions older than 24h are excluded by default)", "現在アクティブな session ID を表示する (既定では 24h 超の stale を除外)"),
		Long: Localize(
			"Print the active matching session ID.\n\nUnlike session latest, this only returns non-ended sessions. By default, sessions older than 24h are treated as stale unless --allow-stale is set.\nFilters resolve as flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_REPO, and --repo falls back to the detected work context when omitted.",
			"条件に一致する active session ID を表示します。\n\nsession latest と違って、未終了の session だけを返します。既定では 24h を超える session は --allow-stale を指定しない限り stale とみなします。\nfilter は flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_REPO の順に解決し、--repo 省略時は検出した work context を使います。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionLatest(cmd.Context(), cmd.OutOrStdout(), sessionLatestCommandInput{
				dbPath:     dbPath,
				client:     client,
				agent:      agent,
				repo:       repo,
				activeOnly: true,
				staleAfter: staleAfter,
				allowStale: allowStale,
				asJSON:     asJSON,
			})
		},
	}
	activeCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	activeCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "記録経路で絞り込む"))
	activeCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "作業主体で絞り込む"))
	activeCmd.Flags().StringVar(&repo, "repo", "", Localize("filter by auxiliary work context identifier", "補助的なコンテキスト識別子で絞り込む"))
	activeCmd.Flags().DurationVar(
		&staleAfter,
		"stale-after",
		defaultActiveSessionStaleAfter,
		Localize("mark active sessions older than this duration as stale", "この duration を超える active session は stale とみなす"),
	)
	activeCmd.Flags().BoolVar(&allowStale, "allow-stale", false, Localize("allow stale sessions to be returned", "stale な session も返す"))
	activeCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return activeCmd
}

func (c *RootCLI) runSessionBoundary(
	ctx context.Context,
	output io.Writer,
	input sessionBoundaryCommandInput,
) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.recordSessionBoundaryUsecase == nil {
		return xerrors.Errorf(Localize("record session boundary usecase is not configured", "session 境界ユースケースが設定されていません"))
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	event, err := c.recordSessionBoundaryUsecase.Run(ctx, usecase.RecordSessionBoundaryInput{
		DBPath:        resolvedPath,
		Client:        resolveSessionBoundaryClient(input),
		DefaultClient: defaultClientValue,
		Agent:         resolveSessionBoundaryAgent(input),
		DefaultAgent:  defaultAgentValue,
		SessionID:     input.sessionID,
		Repo:          resolveSessionBoundaryRepo(input),
		DefaultRepo:   resolveRepoValue(ctx, input.repo),
		Kind:              input.kind,
		Summary:           input.summary,
		ParentSessionID:   input.parentSessionID,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record session boundary", "session 境界の記録に失敗しました"), err)
	}
	if input.asJSON {
		if err := writeEventJSON(output, event); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print session boundary result", "session 境界結果の出力に失敗しました"), err)
		}
		return nil
	}

	if input.kind == types.EventKindSessionEnded {
		if input.idOnly {
			if _, err := fmt.Fprintln(output, event.EventID()); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print session end result", "session end 結果の出力に失敗しました"), err)
			}
			return nil
		}
		if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Recorded", "記録しました"), event.EventID()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print session end result", "session end 結果の出力に失敗しました"), err)
		}
		return nil
	}

	if _, err := fmt.Fprintln(output, event.SessionID()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print session ID", "session ID の出力に失敗しました"), err)
	}

	return nil
}

func resolveSessionBoundaryClient(input sessionBoundaryCommandInput) string {
	if input.kind == types.EventKindSessionEnded {
		return resolveOptionalValue(input.client, "TRACEARY_CLIENT", "")
	}

	return resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue)
}

func resolveSessionBoundaryAgent(input sessionBoundaryCommandInput) string {
	if input.kind == types.EventKindSessionEnded {
		return resolveOptionalValue(input.agent, "TRACEARY_AGENT", "")
	}

	return resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue)
}

func resolveSessionBoundaryRepo(input sessionBoundaryCommandInput) string {
	if input.kind == types.EventKindSessionEnded {
		return resolveExplicitRepoValue(input.repo)
	}

	return resolveRepoValue(context.Background(), input.repo)
}

func (c *RootCLI) runSessionLatest(
	ctx context.Context,
	output io.Writer,
	input sessionLatestCommandInput,
) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.findLatestSessionQueryService == nil {
		return xerrors.Errorf(Localize("find latest session query service is not configured", "直近セッションクエリサービスが設定されていません"))
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	event, err := c.findLatestSessionQueryService.Run(ctx, resolvedPath, queryservice.FindLatestSessionInput{
		Client:     resolveOptionalValue(input.client, "TRACEARY_CLIENT", ""),
		Agent:      resolveOptionalValue(input.agent, "TRACEARY_AGENT", ""),
		Repo:       resolveRepoValue(ctx, input.repo),
		ActiveOnly: input.activeOnly,
	})
	if err != nil {
		if queryservice.IsSessionLookupNotFound(err) {
			if input.activeOnly {
				return xerrors.Errorf(Localize("no matching active session found", "条件に一致する active session は存在しません"))
			}
			return xerrors.Errorf(Localize("no matching session found", "条件に一致する session は存在しません"))
		}
		if input.activeOnly {
			return xerrors.Errorf("%s: %w", Localize("failed to get active session", "アクティブ session の取得に失敗しました"), err)
		}
		return xerrors.Errorf("%s: %w", Localize("failed to get latest session", "直近セッションの取得に失敗しました"), err)
	}
	if err := validateActiveSessionFreshness(event, input); err != nil {
		return err
	}
	if input.asJSON {
		if err := writeEventJSON(output, event); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print session result", "session 結果の出力に失敗しました"), err)
		}
		return nil
	}

	if _, err := fmt.Fprintln(output, event.SessionID()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print session ID", "session ID の出力に失敗しました"), err)
	}

	return nil
}

func validateActiveSessionFreshness(event *model.Event, input sessionLatestCommandInput) error {
	if !input.activeOnly || input.allowStale || input.staleAfter <= 0 || event == nil {
		return nil
	}

	staleCutoff := time.Now().Add(-input.staleAfter)
	if !event.CreatedAt().Before(staleCutoff) {
		return nil
	}

	return xerrors.Errorf(
		Localize(
			"active session %s is older than %s and considered stale; use --allow-stale or close it with session end",
			"active session %s は %s を超えており stale です。--allow-stale を使うか session end で閉じてください",
		),
		event.SessionID(),
		input.staleAfter,
	)
}
