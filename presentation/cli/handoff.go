package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionHandoffCommand() *cobra.Command {
	return c.newHandoffCommandWithUse(
		"handoff",
		Localize("Print a structured session handoff summary", "構造化された session handoff サマリーを出力する"),
	)
}

func (c *RootCLI) newHandoffCommand() *cobra.Command {
	return c.newHandoffCommandWithUse(
		"handoff",
		Localize("Print a structured session handoff summary", "構造化された session handoff サマリーを出力する"),
	)
}

func (c *RootCLI) newHandoffCommandWithUse(use string, short string) *cobra.Command {
	var (
		dbPath    string
		sessionID string
		repo      string
		recent    int
		memories  int
		preset    string
	)

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: Localize(
			"Print a structured working-memory summary for handoff or context resumption.",
			"引き継ぎや文脈再開のための構造化された working-memory サマリーを出力します。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHandoff(cmd.Context(), cmd.OutOrStdout(), handoffCommandInput{
				dbPath:    dbPath,
				sessionID: sessionID,
				workspace: repo,
				recent:    recent,
				memories:  memories,
				preset:    preset,
			})
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID", "セッション ID"))
	cmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().IntVar(&recent, "recent", 5, Localize("number of recent commands to show", "表示する直近コマンド数"))
	cmd.Flags().IntVar(&memories, "memories", 5, Localize("number of durable memories to include", "含める durable memory 数"))
	cmd.Flags().StringVar(&preset, "preset", "", Localize("apply a built-in retrieval preset to durable memories (resume | review | incident)", "durable memory 取得に built-in preset を適用する (resume | review | incident)"))

	return cmd
}

type handoffCommandInput struct {
	dbPath    string
	sessionID string
	workspace string
	recent    int
	memories  int
	preset    string
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
	result, err := c.context.Handoff(
		ctx,
		apptypes.NewContextPackCriteriaBuilder().
			SessionID(types.SessionID(resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", ""))).
			Workspace(types.Workspace(resolvedWorkspace)).
			RecentCommandsLimit(input.recent).
			MemoryLimit(input.memories).
			MemoryPreset(preset).
			Build(),
	)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build handoff summary", "handoff サマリーの構築に失敗しました"), err)
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
		if _, err := fmt.Fprintf(
			output,
			"- [%s][%s:%s] %s\n",
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
