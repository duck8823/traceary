package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionHandoffCommand() *cobra.Command {
	var (
		dbPath      string
		sessionID   string
		repo        string
		recent      int
		memories    int
		preset      string
		asOf        string
		compactOnly bool
		staleAfter  time.Duration
		allowStale  bool
	)

	cmd := &cobra.Command{
		Use:   "handoff",
		Short: Localize("Print a structured session handoff summary", "構造化された session handoff サマリーを出力する"),
		Long: Localize(
			"Print a structured working-memory summary for handoff or context resumption. Pass --compact-only to emit the single-line summary used on session resume.",
			"引き継ぎや文脈再開のための構造化された working-memory サマリーを出力します。--compact-only を指定するとセッション再開で使う 1 行形式を出力します。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if compactOnly {
				// Preserve byte-for-byte parity with the legacy
				// `traceary compact-summary` output: that command
				// defaulted --recent to 3, while the full handoff
				// defaults to 5. If the caller did not explicitly
				// set --recent, fall back to 3 for the compact path.
				compactRecent := recent
				if !cmd.Flags().Changed("recent") {
					compactRecent = compactSummaryDefaultRecent
				}
				resolvedDBPath, err := resolveDBPath(dbPath)
				if err != nil {
					return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
				}
				c.applyDatabasePath(resolvedDBPath)
				if err := c.storeManagement.Initialize(cmd.Context()); err != nil {
					return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
				}
				// Plumb --memories / --preset / --as-of through to
				// the compact path too. The legacy compact-summary
				// command used MemoryLimit == RecentCommandsLimit,
				// so if --memories was NOT explicitly set we keep
				// that legacy behavior; a user-provided --memories
				// wins. --preset and --as-of were not available on
				// the legacy command, so they are None by default.
				memoryLimit := compactRecent
				if cmd.Flags().Changed("memories") {
					memoryLimit = memories
				}
				parsedPreset, err := apptypes.MemoryRetrievalPresetOf(preset)
				if err != nil {
					return xerrors.Errorf("%s: %w", Localize("failed to parse --preset", "--preset の解析に失敗しました"), err)
				}
				parsedAsOf, err := parseOptionalValidityTime(asOf)
				if err != nil {
					return xerrors.Errorf("%s: %w", Localize("failed to parse --as-of", "--as-of の解析に失敗しました"), err)
				}
				return c.printCompactSummaryWithOptions(cmd.Context(), cmd.OutOrStdout(), compactSummaryOptions{
					sessionID:   resolveOptionalValue(sessionID, "TRACEARY_SESSION_ID", ""),
					workspace:   resolveWorkspaceValue(cmd.Context(), repo),
					recentCount: compactRecent,
					memoryLimit: memoryLimit,
					preset:      parsedPreset,
					asOf:        parsedAsOf,
					staleAfter:  staleAfter,
					allowStale:  allowStale,
				})
			}
			return c.runHandoff(cmd.Context(), cmd.OutOrStdout(), handoffCommandInput{
				dbPath:     dbPath,
				sessionID:  sessionID,
				workspace:  repo,
				recent:     recent,
				memories:   memories,
				preset:     preset,
				asOf:       asOf,
				staleAfter: staleAfter,
				allowStale: allowStale,
			})
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID", "セッション ID"))
	cmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().IntVar(&recent, "recent", 5, Localize("number of recent commands to show", "表示する直近コマンド数"))
	cmd.Flags().IntVar(&memories, "memories", 5, Localize("number of durable memories to include", "含める durable memory 数"))
	cmd.Flags().StringVar(&preset, "preset", "", Localize("apply a built-in retrieval preset to durable memories (resume | review | incident)", "durable memory 取得に built-in preset を適用する (resume | review | incident)"))
	cmd.Flags().StringVar(&asOf, "as-of", "", Localize("evaluate durable memory validity at the given timestamp (RFC3339 or YYYY-MM-DD)", "指定時刻 (RFC3339 または YYYY-MM-DD) の時点で durable memory の validity を評価する"))
	cmd.Flags().BoolVar(&compactOnly, "compact-only", false, Localize("emit the short prompt-injection summary used on session resume (replaces the v0.8.x compact-summary command); implicitly sets --recent=3 unless --recent is given", "セッション再開時に使う短い prompt-injection summary を出力する (v0.8.x の compact-summary を置き換え); --recent 未指定時は 3 に自動設定"))
	cmd.Flags().DurationVar(
		&staleAfter,
		"stale-after",
		defaultActiveSessionStaleAfter,
		Localize("treat unended sessions older than this duration as stale", "この duration を超える未終了 session は stale とみなす"),
	)
	cmd.Flags().BoolVar(&allowStale, "allow-stale", false, Localize("allow stale active sessions to be selected", "stale な active session の選択を許可する"))

	return cmd
}

type handoffCommandInput struct {
	dbPath     string
	sessionID  string
	workspace  string
	recent     int
	memories   int
	preset     string
	asOf       string
	staleAfter time.Duration
	allowStale bool
}

func (c *RootCLI) runHandoff(ctx context.Context, output io.Writer, input handoffCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.context == nil {
		return xerrors.Errorf(Localize("context usecase is not configured", "context ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	resolvedWorkspace := resolveWorkspaceValue(ctx, input.workspace)
	preset, err := apptypes.MemoryRetrievalPresetOf(input.preset)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --preset", "--preset の解析に失敗しました"), err)
	}
	asOf, err := parseOptionalValidityTime(input.asOf)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --as-of", "--as-of の解析に失敗しました"), err)
	}
	resolvedSessionID := types.SessionID(resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", ""))
	baseBuilder := apptypes.NewContextPackCriteriaBuilder().
		SessionID(resolvedSessionID).
		Workspace(types.Workspace(resolvedWorkspace)).
		RecentCommandsLimit(input.recent).
		MemoryLimit(input.memories).
		MemoryPreset(preset).
		MemoryAsOf(asOf).
		StaleAfter(input.staleAfter).
		AllowStale(input.allowStale)
	result, err := c.context.Handoff(ctx, baseBuilder.Build())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build handoff summary", "handoff サマリーの構築に失敗しました"), err)
	}

	if _, ok := result.Value(); !ok && !input.allowStale && input.staleAfter > 0 {
		// The builder may have skipped a stale active session. Re-query
		// with allowStale=true so we can surface a specific hint instead
		// of the generic "no matching session" message — this is only a
		// best-effort lookup; an error here falls through to the empty
		// output below so the user still sees a reasonable response.
		recheck, recheckErr := c.context.Handoff(ctx, baseBuilder.AllowStale(true).Build())
		if recheckErr == nil {
			if pack, ok := recheck.Value(); ok {
				return xerrors.Errorf(
					Localize(
						"active session %s is older than %s and considered stale; pass --allow-stale or close it with session end",
						"active session %s は %s を超えており stale です。--allow-stale を指定するか session end で閉じてください",
					),
					pack.SessionID(),
					input.staleAfter,
				)
			}
		}
	}

	return writeHandoffText(output, result)
}

func writeHandoffText(output io.Writer, result types.Optional[apptypes.ContextPack]) error {
	if _, ok := result.Value(); !ok {
		if _, err := fmt.Fprintln(output, Localize("No matching session handoff.", "一致する session handoff はありません。")); err != nil {
			return xerrors.Errorf("failed to print empty handoff output: %w", err)
		}
		return nil
	}

	pack, _ := result.Value()
	if _, err := fmt.Fprintln(output, "TRACEARY HANDOFF"); err != nil {
		return xerrors.Errorf("failed to print handoff header: %w", err)
	}
	if _, err := fmt.Fprintf(output, "SESSION_ID: %s\n", pack.SessionID()); err != nil {
		return xerrors.Errorf("failed to print handoff session ID: %w", err)
	}
	if _, err := fmt.Fprintf(output, "WORKSPACE: %s\n", formatOptionalColumn(pack.Workspace().String())); err != nil {
		return xerrors.Errorf("failed to print handoff workspace: %w", err)
	}
	if pack.WorkspaceFallbackUsed() {
		if _, err := fmt.Fprintf(
			output,
			"NOTE: matched through parent workspace %s (requested %s)\n",
			pack.Workspace().String(),
			pack.RequestedWorkspace().String(),
		); err != nil {
			return xerrors.Errorf("failed to print handoff workspace fallback note: %w", err)
		}
	}
	if _, err := fmt.Fprintf(output, "LABEL: %s\n", formatOptionalColumn(pack.Label())); err != nil {
		return xerrors.Errorf("failed to print handoff label: %w", err)
	}
	if _, err := fmt.Fprintf(output, "STATUS: %s\n", formatOptionalColumn(pack.Status())); err != nil {
		return xerrors.Errorf("failed to print handoff status: %w", err)
	}
	if _, err := fmt.Fprintf(output, "TOTAL_EVENTS: %d\n", pack.TotalEvents()); err != nil {
		return xerrors.Errorf("failed to print handoff total events: %w", err)
	}
	if _, err := fmt.Fprintf(output, "COMMAND_COUNT: %d\n", pack.CommandCount()); err != nil {
		return xerrors.Errorf("failed to print handoff command count: %w", err)
	}
	if _, err := fmt.Fprintf(output, "AGENTS: %s\n", formatOptionalColumn(strings.Join(pack.Agents(), ", "))); err != nil {
		return xerrors.Errorf("failed to print handoff agents: %w", err)
	}
	if _, err := fmt.Fprintln(output, "WORKING_STATE:"); err != nil {
		return xerrors.Errorf("failed to print working-state heading: %w", err)
	}
	if _, err := fmt.Fprintf(output, "- session_summary: %s\n", formatOptionalColumn(pack.WorkingState().SessionSummary())); err != nil {
		return xerrors.Errorf("failed to print handoff session summary: %w", err)
	}
	if _, err := fmt.Fprintf(output, "- compact_summary: %s\n", formatOptionalColumn(pack.WorkingState().CompactSummary())); err != nil {
		return xerrors.Errorf("failed to print handoff compact summary: %w", err)
	}
	if _, err := fmt.Fprintln(output, "RECENT_COMMANDS:"); err != nil {
		return xerrors.Errorf("failed to print recent-commands heading: %w", err)
	}
	for _, command := range pack.RecentCommands() {
		if _, err := fmt.Fprintf(output, "- %s\n", command); err != nil {
			return xerrors.Errorf("failed to print handoff recent command: %w", err)
		}
	}
	if len(pack.RecentCommands()) == 0 {
		if _, err := fmt.Fprintln(output, "-"); err != nil {
			return xerrors.Errorf("failed to print empty recent-commands item: %w", err)
		}
	}
	if _, err := fmt.Fprintln(output, "MEMORIES:"); err != nil {
		return xerrors.Errorf("failed to print memories heading: %w", err)
	}
	for _, memory := range pack.Memories() {
		// Tag candidate (and any non-accepted) entries with a leading
		// status marker so the reader can tell pending review items
		// apart from curated ones. Accepted entries keep their
		// established two-bracket layout.
		statusPrefix := ""
		if memory.Status() != types.MemoryStatusAccepted {
			statusPrefix = fmt.Sprintf("[%s]", memory.Status())
		}
		if _, err := fmt.Fprintf(
			output,
			"- %s[%s][%s:%s] %s\n",
			statusPrefix,
			memory.MemoryType(),
			memory.Scope().Kind(),
			memory.Scope().Key(),
			memory.Fact(),
		); err != nil {
			return xerrors.Errorf("failed to print handoff memory: %w", err)
		}
	}
	if len(pack.Memories()) == 0 {
		if _, err := fmt.Fprintln(output, "-"); err != nil {
			return xerrors.Errorf("failed to print empty memories item: %w", err)
		}
	}

	return nil
}
