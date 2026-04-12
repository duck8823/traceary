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
		fmt.Fprintf(&sb, "Session %s resumed after compact\n", s.SessionID())
		if s.Workspace().String() != "" {
			fmt.Fprintf(&sb, "  repo: %s\n", s.Workspace())
		}
		if s.Label() != "" {
			fmt.Fprintf(&sb, "  label: %s\n", s.Label())
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

	// Retrieve the latest compact_summary event for richer context.
	// Errors are intentionally ignored: this output is injected into hook stdout
	// and must not fail even if the compact_summary query encounters a DB issue.
	compactSummaryEvents, err := c.event.List(ctx, usecase.EventListCriteria{
		Limit:     1,
		SessionID: types.SessionID(sessionID),
		Workspace: types.Workspace(repo),
		Kind:      types.EventKindCompactSummary,
	})
	if err == nil && len(compactSummaryEvents) > 0 {
		body := compactSummaryEvents[0].Body()
		summary := extractCompactSummarySections(body)
		if summary != "" {
			sb.WriteString("  summary: ")
			sb.WriteString(summary)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("  Run list_events for full history.\n")

	if _, err := fmt.Fprint(output, sb.String()); err != nil {
		return xerrors.Errorf("failed to print compact summary: %w", err)
	}
	return nil
}

const maxCompactSummaryLen = 500

// extractCompactSummarySections extracts "Current Work" and "Pending Tasks"
// sections from a compact_summary body. Returns a truncated single-line string.
func extractCompactSummarySections(body string) string {
	sections := []string{"Current Work", "Pending Tasks"}
	var parts []string

	for _, section := range sections {
		header := fmt.Sprintf("%s:", section)
		idx := strings.Index(body, header)
		if idx < 0 {
			// Try numbered section format (e.g., "8. Current Work:")
			for i := 1; i <= 9; i++ {
				alt := fmt.Sprintf("%d. %s:", i, section)
				idx = strings.Index(body, alt)
				if idx >= 0 {
					idx += len(alt)
					break
				}
			}
			if idx < 0 {
				continue
			}
		} else {
			idx += len(header)
		}

		// Extract content until next section header or end
		rest := body[idx:]
		endIdx := len(rest)
		for i := 1; i <= 9; i++ {
			nextHeader := fmt.Sprintf("\n%d. ", i)
			if pos := strings.Index(rest, nextHeader); pos >= 0 && pos < endIdx {
				endIdx = pos
			}
		}
		// Also check for plain (unnumbered) section headers
		for _, otherSection := range sections {
			if otherSection == section {
				continue
			}
			plainHeader := "\n" + otherSection + ":"
			if pos := strings.Index(rest, plainHeader); pos >= 0 && pos < endIdx {
				endIdx = pos
			}
		}
		content := strings.TrimSpace(rest[:endIdx])
		// Collapse to single line
		content = strings.Join(strings.Fields(content), " ")
		if content != "" {
			parts = append(parts, content)
		}
	}

	result := strings.Join(parts, " | ")
	if len([]rune(result)) > maxCompactSummaryLen {
		result = string([]rune(result)[:maxCompactSummaryLen]) + "…"
	}
	return result
}
