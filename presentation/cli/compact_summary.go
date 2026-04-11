package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
	"github.com/duck8823/traceary/application/usecase"

	"github.com/duck8823/traceary/domain/types"

)

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
			if err := c.storeMaintenance.Initialize(ctx); err != nil {
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
	// Get recent events for context
	events, err := c.event.List(ctx, usecase.EventListCriteria{
		Limit:     recentCount + 5, // fetch extra to find commands
		SessionID: types.SessionID(sessionID),
		Workspace: types.Workspace(repo),
		Kind:      types.EventKindCommandExecuted,
	})
	if err != nil {
		return xerrors.Errorf("failed to list events: %w", err)
	}

	// Get session info
	sessions, err := c.session.List(ctx, usecase.SessionListCriteria{
		Limit:     1,
		SessionID: types.SessionID(sessionID),
		Workspace: types.Workspace(repo),
	})
	if err != nil {
		return xerrors.Errorf("failed to list sessions: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("[Traceary] ")

	if len(sessions) > 0 {
		s := sessions[0]
		fmt.Fprintf(&sb, "Session %s resumed after compact\n", s.SessionID)
		if s.Workspace != "" {
			fmt.Fprintf(&sb, "  repo: %s\n", s.Workspace)
		}
		if s.Label != "" {
			fmt.Fprintf(&sb, "  label: %s\n", s.Label)
		}
	} else {
		sb.WriteString("No active session\n")
	}

	if len(events) > 0 {
		sb.WriteString("  recent: ")
		shown := 0
		for _, e := range events {
			if shown >= recentCount {
				break
			}
			if shown > 0 {
				sb.WriteString(" → ")
			}
			cmd := truncateMessage(e.Body())
			if len([]rune(cmd)) > 40 {
				cmd = string([]rune(cmd)[:40]) + "…"
			}
			sb.WriteString(cmd)
			shown++
		}
		sb.WriteString("\n")
	}

	sb.WriteString("  Run list_events for full history.\n")

	if _, err := fmt.Fprint(output, sb.String()); err != nil {
		return xerrors.Errorf("failed to print compact summary: %w", err)
	}
	return nil
}
