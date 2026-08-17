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

func (c *RootCLI) newContextCommand() *cobra.Command {
	var (
		dbPath            string
		sessionID         string
		client            string
		agent             string
		repo              string
		limit             int
		asJSON            bool
		handoff           bool
		recent            int
		memories          int
		preset            string
		includeCandidates bool
		asOf              string
		compactOnly       bool
		staleAfter        time.Duration
		allowStale        bool
	)

	contextCmd := &cobra.Command{
		Use:   "context",
		Short: Localize("Print raw recent context events, or a structured handoff with --handoff", "次の AI session に渡す生の recent context event を表示する。--handoff で構造化サマリー"),
		Long: Localize(
			"Print raw recent context events for the next AI session. Pass --handoff for the structured working-memory pack (TRACEARY HANDOFF labels). Pass --compact-only for the single-line resume summary. --handoff and --compact-only are mutually exclusive.",
			"次の AI session に渡す生の recent context event を表示します。--handoff で構造化 working-memory pack（TRACEARY HANDOFF ラベル）を出します。--compact-only でセッション再開用の 1 行サマリーを出します。--handoff と --compact-only は同時に使えません。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateContextModeFlags(cmd, handoff, compactOnly); err != nil {
				return err
			}
			if compactOnly {
				return c.runCompactSummaryCommand(cmd.Context(), cmd.OutOrStdout(), compactSummaryCommandInput{
					dbPath:            dbPath,
					sessionID:         sessionID,
					workspace:         repo,
					recent:            recent,
					memories:          memories,
					recentChanged:     cmd.Flags().Changed("recent"),
					memoriesChanged:   cmd.Flags().Changed("memories"),
					preset:            preset,
					includeCandidates: includeCandidates,
					asOf:              asOf,
					staleAfter:        staleAfter,
					allowStale:        allowStale,
				})
			}
			if handoff {
				return c.runHandoff(cmd.Context(), cmd.OutOrStdout(), handoffCommandInput{
					dbPath:            dbPath,
					sessionID:         sessionID,
					workspace:         repo,
					recent:            recent,
					memories:          memories,
					preset:            preset,
					includeCandidates: includeCandidates,
					asOf:              asOf,
					staleAfter:        staleAfter,
					allowStale:        allowStale,
				})
			}
			return c.runContext(cmd.Context(), cmd.OutOrStdout(), contextCommandInput{
				dbPath:    dbPath,
				sessionID: sessionID,
				client:    client,
				agent:     agent,
				repo:      repo,
				limit:     limit,
				asJSON:    asJSON,
			})
		},
	}
	contextCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	contextCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("target session ID", "対象の session ID"))
	contextCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "作業主体の入口で絞り込む"))
	contextCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "作業主体で絞り込む"))
	contextCmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by auxiliary workspace identifier", "補助的な workspace 識別子で絞り込む"))
	contextCmd.Flags().IntVar(&limit, "limit", 10, Localize("maximum number of events to include", "表示件数"))
	contextCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output (raw context only)", "JSON 形式で出力する（生 context のみ）"))
	contextCmd.Flags().BoolVar(&handoff, "handoff", false, Localize("print the structured working-memory pack (TRACEARY HANDOFF)", "構造化 working-memory pack（TRACEARY HANDOFF）を出力する"))
	contextCmd.Flags().IntVar(&recent, "recent", 5, Localize("with --handoff/--compact-only, number of recent commands to show", "--handoff/--compact-only 時に表示する直近コマンド数"))
	contextCmd.Flags().IntVar(&memories, "memories", 5, Localize("with --handoff/--compact-only, number of durable memories to include", "--handoff/--compact-only 時に含める durable memory 数"))
	contextCmd.Flags().StringVar(&preset, "preset", "", Localize("with --handoff/--compact-only, apply a built-in retrieval preset (resume | review | incident)", "--handoff/--compact-only 時に durable memory 取得へ built-in preset を適用する (resume | review | incident)"))
	contextCmd.Flags().BoolVar(&includeCandidates, "include-candidates", false, Localize("with --handoff/--compact-only, include memory candidates in a separate needs-review section", "--handoff/--compact-only 時にメモリ候補を別の needs-review セクションに含める"))
	contextCmd.Flags().StringVar(&asOf, "as-of", "", Localize("with --handoff/--compact-only, evaluate durable memory validity at the given timestamp (RFC3339 or YYYY-MM-DD)", "--handoff/--compact-only 時に指定時刻 (RFC3339 または YYYY-MM-DD) の時点で durable memory の validity を評価する"))
	contextCmd.Flags().BoolVar(&compactOnly, "compact-only", false, Localize("emit the short prompt-injection summary used on session resume; implicitly sets --recent=3 unless --recent is given", "セッション再開時に使う短い prompt-injection summary を出力する; --recent 未指定時は 3 に自動設定"))
	contextCmd.Flags().DurationVar(
		&staleAfter,
		"stale-after",
		defaultActiveSessionStaleAfter,
		Localize("with --handoff/--compact-only, treat unended sessions older than this duration as stale", "--handoff/--compact-only 時、この duration を超える未終了 session は stale とみなす"),
	)
	contextCmd.Flags().BoolVar(&allowStale, "allow-stale", false, Localize("with --handoff/--compact-only, allow stale active sessions to be selected", "--handoff/--compact-only 時、stale な active session の選択を許可する"))
	contextCmd.MarkFlagsMutuallyExclusive("handoff", "compact-only")
	contextCmd.MarkFlagsMutuallyExclusive("json", "handoff")
	contextCmd.MarkFlagsMutuallyExclusive("json", "compact-only")

	return contextCmd
}

func validateContextModeFlags(cmd *cobra.Command, handoff bool, compactOnly bool) error {
	handoffOnlyChanged := cmd.Flags().Changed("recent") ||
		cmd.Flags().Changed("memories") ||
		cmd.Flags().Changed("preset") ||
		cmd.Flags().Changed("include-candidates") ||
		cmd.Flags().Changed("as-of") ||
		cmd.Flags().Changed("stale-after") ||
		cmd.Flags().Changed("allow-stale")
	if !handoff && !compactOnly {
		if handoffOnlyChanged {
			return xerrors.New(Localize(
				"--recent/--memories/--preset/--include-candidates/--as-of/--stale-after/--allow-stale require --handoff or --compact-only",
				"--recent/--memories/--preset/--include-candidates/--as-of/--stale-after/--allow-stale には --handoff または --compact-only が必要です",
			))
		}
		return nil
	}
	if cmd.Flags().Changed("limit") {
		return xerrors.New(Localize(
			"--limit cannot be combined with --handoff or --compact-only",
			"--limit は --handoff / --compact-only と同時に使えません",
		))
	}
	return nil
}

func (c *RootCLI) runContext(ctx context.Context, output io.Writer, input contextCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.New(Localize("get context query service is not configured", "文脈クエリサービスが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.New(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	resolvedWorkspace := resolveWorkspaceValue(ctx, input.repo)
	resolvedSessionID, err := c.resolveContextSessionID(ctx, contextCommandInput{
		sessionID: input.sessionID,
		client:    input.client,
		agent:     input.agent,
		repo:      resolvedWorkspace,
	})
	if err != nil {
		return err
	}

	contextCriteria := apptypes.NewEventContextCriteriaBuilder(input.limit).
		Workspace(types.Workspace(resolvedWorkspace)).
		SessionID(types.SessionID(resolvedSessionID)).
		Build()
	events, err := c.event.Context(ctx, contextCriteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to get context", "文脈の取得に失敗しました"), err)
	}
	if err := c.hydrateCommandLinesForDisplay(ctx, events); err != nil {
		return err
	}

	if input.asJSON {
		return writeContextJSON(output, resolvedSessionID, resolvedWorkspace, events)
	}

	return writeContextText(output, resolvedSessionID, resolvedWorkspace, events)
}

func (c *RootCLI) resolveContextSessionID(
	ctx context.Context,
	input contextCommandInput,
) (string, error) {
	trimmedSessionID := strings.TrimSpace(input.sessionID)
	if trimmedSessionID != "" {
		return trimmedSessionID, nil
	}
	if c.session == nil {
		slog.Debug("no query service configured for context session resolution")
		return "", nil
	}

	lookupCriteria := apptypes.NewSessionLookupCriteriaBuilder().
		Client(types.Client(strings.TrimSpace(input.client))).
		Agent(types.Agent(strings.TrimSpace(input.agent))).
		Workspace(types.Workspace(strings.TrimSpace(input.repo))).
		Build()
	result, err := c.session.Active(ctx, lookupCriteria)
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve latest session for context", "文脈用の直近 session 解決に失敗しました"), err)
	}
	if _, ok := result.Value(); !ok {
		slog.Debug("no session found for context, using empty session", "client", input.client, "agent", input.agent, "workspace", input.repo)
		return "", nil
	}

	event, _ := result.Value()
	return event.SessionID().String(), nil
}

func writeContextJSON(output io.Writer, sessionID string, repo string, events []*model.Event) error {
	serializedEvents := make([]event, 0, len(events))
	for _, e := range events {
		serializedEvents = append(serializedEvents, newEventOutput(e))
	}

	return writeJSON(output, contextOutput{
		ResolvedSessionID: sessionID,
		ResolvedWorkspace: repo,
		Events:            serializedEvents,
	})
}

func writeContextText(output io.Writer, sessionID string, repo string, events []*model.Event) error {
	if _, err := fmt.Fprintln(output, "TRACEARY CONTEXT"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print context header", "文脈ヘッダーの出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "SESSION_ID: %s\n", formatOptionalColumn(sessionID)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print session ID", "session ID の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "WORKSPACE: %s\n", formatOptionalColumn(repo)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print workspace", "workspace の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintln(output, "EVENTS:"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print context events heading", "文脈イベント見出しの出力に失敗しました"), err)
	}
	if len(events) == 0 {
		if _, err := fmt.Fprintln(output, Localize("- No matching context.", "- 一致する文脈はありません")); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty context message", "空文脈メッセージの出力に失敗しました"), err)
		}
		return nil
	}

	for _, event := range events {
		hookSuffix := ""
		if hook := event.SourceHook(); hook != "" {
			hookSuffix = " (hook=" + hook + ")"
		}
		if _, err := fmt.Fprintf(
			output,
			"- %s [%s]%s %s %s/%s %s\n",
			event.CreatedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
			event.Kind(),
			hookSuffix,
			event.EventID(),
			formatOptionalColumn(event.Client().String()),
			event.Agent(),
			singleLineSummary(apptypes.ExtractPlainBody(event.Body())),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print context event", "文脈イベントの出力に失敗しました"), err)
		}
	}

	return nil
}

func singleLineSummary(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "-"
	}

	return strings.Join(fields, " ")
}
