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

const maxCompactSummaryOutputLen = 560

// compactSummaryDefaultRecent is the legacy default for --recent on
// `traceary compact-summary`. `traceary session handoff --compact-only`
// falls back to this value when the caller does not set --recent, to
// keep the compact output byte-for-byte compatible with v0.8.x.
const compactSummaryDefaultRecent = 3

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
	cmd.Flags().IntVar(&limit, "recent", compactSummaryDefaultRecent, Localize("number of recent commands to show", "表示する直近コマンド数"))

	applyDeprecation(cmd, Localize(
		"use `traceary session handoff --compact-only` — this alias will be removed in v1.0",
		"`traceary session handoff --compact-only` を使ってください — この alias は v1.0 で削除されます",
	))

	return cmd
}

// compactSummaryOptions captures every knob the compact-summary path
// supports. Callers from `session handoff --compact-only` can thread
// --memories / --preset / --as-of through here so those flags are not
// silent no-ops (Codex verifier MUST on #697). The legacy
// `compact-summary` command passes zero / None for the new fields so
// its byte-for-byte output stays unchanged.
type compactSummaryOptions struct {
	sessionID   string
	workspace   string
	recentCount int
	memoryLimit int
	preset      apptypes.MemoryRetrievalPreset
	asOf        types.Optional[time.Time]
}

func (c *RootCLI) printCompactSummary(
	ctx context.Context,
	output io.Writer,
	_ string, // dbPath (resolved at Datasource construction)
	sessionID string,
	repo string,
	recentCount int,
) error {
	return c.printCompactSummaryWithOptions(ctx, output, compactSummaryOptions{
		sessionID:   sessionID,
		workspace:   repo,
		recentCount: recentCount,
		memoryLimit: recentCount,
	})
}

func (c *RootCLI) printCompactSummaryWithOptions(
	ctx context.Context,
	output io.Writer,
	opts compactSummaryOptions,
) error {
	if c.context == nil {
		return xerrors.Errorf("context usecase is not configured")
	}

	result, err := c.context.Handoff(
		ctx,
		apptypes.NewContextPackCriteriaBuilder().
			SessionID(types.SessionID(opts.sessionID)).
			Workspace(types.Workspace(opts.workspace)).
			RecentCommandsLimit(opts.recentCount).
			MemoryLimit(opts.memoryLimit).
			MemoryPreset(opts.preset).
			MemoryAsOf(opts.asOf).
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
	if _, ok := result.Value(); !ok {
		sb.WriteString("No active session\n")
		sb.WriteString("  Run list_events for full history.\n")
		return sb.String(), nil
	}

	pack, _ := result.Value()
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
			// Mark non-accepted entries so the resuming agent does not
			// treat candidate facts as curated (parity with handoff
			// text format — see #812).
			if memory.Status() != types.MemoryStatusAccepted {
				sb.WriteString("[")
				sb.WriteString(memory.Status().String())
				sb.WriteString("] ")
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
