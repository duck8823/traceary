package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
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
	sessionCmd.AddCommand(c.newSessionLineageCommand())
	sessionCmd.AddCommand(c.newSessionGCCommand())

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
			"Print the latest matching session ID.\n\n\"latest\" means the session whose most recent lifecycle boundary (start or end) is newest among the matches.\nFilters resolve as flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_WORKSPACE, and --workspace falls back to the detected workspace when omitted.",
			"条件に一致する直近の session ID を表示します。\n\nここでの「直近」は、一致した session のうち最新の lifecycle boundary (start または end) が最も新しいものを意味します。\nfilter は flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_WORKSPACE の順に解決し、--workspace 省略時は検出した workspace を使います。",
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
	latestCmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by auxiliary workspace identifier", "補助的な workspace 識別子で絞り込む"))
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
			"Record a session-start boundary.\n\nDefaults:\n- DB path: --db-path -> TRACEARY_DB_PATH -> ~/.config/traceary/traceary.db\n- client / agent / workspace: flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_WORKSPACE -> cli / manual / detected workspace\n- session ID: generate a new ID when --session-id is omitted",
			"session 開始境界を記録します。\n\n既定値の解決順:\n- DB path: --db-path -> TRACEARY_DB_PATH -> ~/.config/traceary/traceary.db\n- client / agent / workspace: flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_WORKSPACE -> cli / manual / 検出した workspace\n- session ID: --session-id を省略した場合は新しく採番します",
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
	startCmd.Flags().StringVar(&repo, "workspace", "", Localize("auxiliary workspace identifier (env: TRACEARY_WORKSPACE)", "補助的な workspace 識別子 (env: TRACEARY_WORKSPACE)"))
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
			"Record a session-end boundary.\n\nDefaults:\n- session ID: --session-id -> TRACEARY_SESSION_ID\n- client / agent / workspace: use explicit flags first, then backfill from the matching session start when possible",
			"session 終了境界を記録します。\n\n既定値の解決順:\n- session ID: --session-id -> TRACEARY_SESSION_ID\n- client / agent / workspace: 明示 flag を優先し、足りない値は対応する session start から補完します",
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
	endCmd.Flags().StringVar(&repo, "workspace", "", Localize("auxiliary workspace identifier (env: TRACEARY_WORKSPACE)", "補助的な workspace 識別子 (env: TRACEARY_WORKSPACE)"))
	endCmd.Flags().StringVar(&summary, "summary", "", Localize("session summary text", "セッションサマリーテキスト"))
	endCmd.Flags().BoolVar(&idOnly, "id-only", false, Localize("print only the resulting identifier", "結果の識別子だけを出力する"))
	endCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	endCmd.MarkFlagsMutuallyExclusive("id-only", "json")

	return endCmd
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
			"Print the active matching session ID.\n\nUnlike session latest, this only returns non-ended sessions. By default, sessions older than 24h are treated as stale unless --allow-stale is set.\nFilters resolve as flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_WORKSPACE, and --workspace falls back to the detected workspace when omitted.",
			"条件に一致する active session ID を表示します。\n\nsession latest と違って、未終了の session だけを返します。既定では 24h を超える session は --allow-stale を指定しない限り stale とみなします。\nfilter は flag -> TRACEARY_CLIENT / TRACEARY_AGENT / TRACEARY_WORKSPACE の順に解決し、--workspace 省略時は検出した workspace を使います。",
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
	activeCmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by auxiliary workspace identifier", "補助的な workspace 識別子で絞り込む"))
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
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.session == nil {
		return xerrors.New(Localize("record session boundary usecase is not configured", "session 境界ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	client := types.Client(resolveSessionBoundaryClient(input))
	if client == "" {
		client = types.Client(defaultClientValue)
	}
	agentStr := resolveSessionBoundaryAgent(input)
	if agentStr == "" {
		agentStr = defaultAgentValue
	}
	agent, _ := types.AgentFrom(agentStr)
	sid := types.SessionID(input.sessionID)
	ws := types.Workspace(resolveSessionBoundaryRepo(input))
	if ws == "" {
		ws = types.Workspace(resolveWorkspaceValue(ctx, input.repo))
	}

	var event *model.Event
	switch input.kind {
	case types.EventKindSessionStarted:
		event, err = c.session.Start(ctx, client, agent, sid, ws, types.SessionID(input.parentSessionID))
	case types.EventKindSessionEnded:
		event, err = c.session.End(ctx, client, agent, sid, ws, input.summary)
	default:
		return xerrors.Errorf("unsupported session boundary kind: %s", input.kind)
	}
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record session boundary", "session 境界の記録に失敗しました"), err)
	}
	// CLI parity with the hook session-end auto-extract path (#810,
	// follow-up #830). Best-effort: an extraction failure must not
	// block the session-end record from being reported.
	if input.kind == types.EventKindSessionEnded && c.memory != nil {
		if _, extractErr := c.memory.Extract(ctx, apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(sid).
			Workspace(ws).
			Build()); extractErr != nil {
			slog.Debug("CLI session-end auto-extract failed", "session_id", sid, "error", extractErr)
		}
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
		return resolveExplicitWorkspaceValue(input.repo)
	}

	return resolveWorkspaceValue(context.Background(), input.repo)
}

func (c *RootCLI) runSessionLatest(
	ctx context.Context,
	output io.Writer,
	input sessionLatestCommandInput,
) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.session == nil {
		return xerrors.New(Localize("find latest session query service is not configured", "直近セッションクエリサービスが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	criteria := apptypes.NewSessionLookupCriteriaBuilder().
		Client(types.Client(resolveOptionalValue(input.client, "TRACEARY_CLIENT", ""))).
		Agent(types.Agent(resolveOptionalValue(input.agent, "TRACEARY_AGENT", ""))).
		Workspace(types.Workspace(resolveWorkspaceValue(ctx, input.repo))).
		Build()
	var result types.Optional[*model.Event]
	if input.activeOnly {
		result, err = c.session.Active(ctx, criteria)
	} else {
		result, err = c.session.Latest(ctx, criteria)
	}
	if err != nil {
		if input.activeOnly {
			return xerrors.Errorf("%s: %w", Localize("failed to get active session", "アクティブ session の取得に失敗しました"), err)
		}
		return xerrors.Errorf("%s: %w", Localize("failed to get latest session", "直近セッションの取得に失敗しました"), err)
	}
	if _, ok := result.Value(); !ok {
		if input.activeOnly {
			return xerrors.New(Localize("no matching active session found", "条件に一致する active session は存在しません"))
		}
		return xerrors.New(Localize("no matching session found", "条件に一致する session は存在しません"))
	}
	event, _ := result.Value()
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
