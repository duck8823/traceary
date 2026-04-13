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

const maxCompactSummaryOutputLen = 560

func (c *RootCLI) newCompactSummaryCommand() *cobra.Command {
	var (
		dbPath    string
		sessionID string
		repo      string
		limit     int
	)

	cmd := &cobra.Command{
		Use:   "compact-summary",
		Short: Localize("Generate a compact context pointer for session resumption", "compact 後のセッション再開用コンテキストポインタを生成する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			output := cmd.OutOrStdout()

			resolvedDBPath, err := resolveDBPath(dbPath)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
			}
			c.applyDatabasePath(resolvedDBPath)
			if err := c.storeManagement.Initialize(ctx); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
			}

			resolvedRepo := resolveWorkspaceValue(ctx, repo)
			resolvedSessionID := resolveOptionalValue(sessionID, "TRACEARY_SESSION_ID", "")

			return c.printCompactSummary(ctx, output, resolvedDBPath, resolvedSessionID, resolvedRepo, limit)
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&sessionID, "session-id", "", Localize("session ID", "セッション ID"))
	cmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().IntVar(&limit, "recent", 3, Localize("number of recent commands to show", "表示する直近コマンド数"))

	return cmd
}

func (c *RootCLI) printCompactSummary(
	ctx context.Context,
	output io.Writer,
	_ string, // dbPath (resolved at Datasource construction)
	sessionID string,
	repo string,
	recentCount int,
) error {
	if c.context == nil {
		return xerrors.Errorf("context usecase is not configured")
	}

	result, err := c.context.Handoff(
		ctx,
		apptypes.NewContextPackCriteriaBuilder().
			SessionID(types.SessionID(sessionID)).
			Workspace(types.Workspace(repo)).
			RecentCommandsLimit(recentCount).
			MemoryLimit(recentCount).
			Build(),
	)
	if err != nil {
		return xerrors.Errorf("failed to build compact summary: %w", err)
	}

	text, err := buildCompactSummaryText(result)
	if err != nil {
		return xerrors.Errorf("failed to render compact summary: %w", err)
	}
	if _, err := fmt.Fprint(output, text); err != nil {
		return xerrors.Errorf("failed to print compact summary: %w", err)
	}
	return nil
}

func buildCompactSummaryText(result types.Optional[apptypes.ContextPack]) (string, error) {
	var sb strings.Builder
	sb.WriteString("[Traceary] ")
	if !result.IsPresent() {
		sb.WriteString("No active session\n")
		sb.WriteString("  Run list_events for full history.\n")
		return sb.String(), nil
	}

	pack, _ := result.Get()
	fmt.Fprintf(&sb, "Session %s resumed after compact\n", pack.SessionID())
	if pack.Workspace().String() != "" {
		fmt.Fprintf(&sb, "  workspace: %s\n", pack.Workspace())
	}
	if pack.Label() != "" {
		fmt.Fprintf(&sb, "  label: %s\n", pack.Label())
	}
	if summary := pack.WorkingState().CombinedSummary(); summary != "" {
		fmt.Fprintf(&sb, "  summary: %s\n", truncateCompactSummarySegment(summary, 160))
	}
	if commands := pack.RecentCommands(); len(commands) > 0 {
		sb.WriteString("  recent: ")
		for index, command := range commands {
			if index > 0 {
				sb.WriteString(" → ")
			}
			sb.WriteString(truncateCompactSummarySegment(command, 40))
		}
		sb.WriteString("\n")
	}
	if memories := pack.Memories(); len(memories) > 0 {
		sb.WriteString("  memories: ")
		for index, memory := range memories {
			if index > 0 {
				sb.WriteString(" | ")
			}
			sb.WriteString(truncateCompactSummarySegment(memory.Fact(), 60))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("  Run list_events for full history.\n")
	text := sb.String()
	if runes := []rune(text); len(runes) > maxCompactSummaryOutputLen {
		text = string(runes[:maxCompactSummaryOutputLen]) + "…\n"
	}
	return text, nil
}

func truncateCompactSummarySegment(value string, limit int) string {
	if runes := []rune(value); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}
