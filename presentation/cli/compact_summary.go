package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const maxCompactSummaryOutputLen = 560

// compactSummaryDefaultRecent is the default --recent value used by
// `traceary context --compact-only`. It matches the v0.8.x
// `traceary compact-summary` default (removed in v0.14.0) so the
// compact output stays byte-for-byte compatible.
const compactSummaryDefaultRecent = 3

// compactSummaryOptions captures every knob the compact-summary path
// supports. `context --compact-only` threads --memories /
// --preset / --as-of through here so those flags are not silent
// no-ops.
type compactSummaryCommandInput struct {
	dbPath            string
	sessionID         string
	workspace         string
	recent            int
	memories          int
	recentChanged     bool
	memoriesChanged   bool
	preset            string
	includeCandidates bool
	asOf              string
	staleAfter        time.Duration
	allowStale        bool
}

func (c *RootCLI) runCompactSummaryCommand(ctx context.Context, output io.Writer, input compactSummaryCommandInput) error {
	// Preserve byte-for-byte parity with the legacy
	// `traceary compact-summary` output: that command
	// defaulted --recent to 3, while the full handoff
	// defaults to 5. If the caller did not explicitly
	// set --recent, fall back to 3 for the compact path.
	compactRecent := input.recent
	if !input.recentChanged {
		compactRecent = compactSummaryDefaultRecent
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
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
	if input.memoriesChanged {
		memoryLimit = input.memories
	}
	parsedPreset, err := apptypes.MemoryRetrievalPresetOf(input.preset)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --preset", "--preset の解析に失敗しました"), err)
	}
	parsedAsOf, err := parseOptionalValidityTime(input.asOf)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --as-of", "--as-of の解析に失敗しました"), err)
	}
	return c.printCompactSummaryWithOptions(ctx, output, compactSummaryOptions{
		sessionID:         resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", ""),
		workspace:         resolveWorkspaceValue(ctx, input.workspace),
		recentCount:       compactRecent,
		memoryLimit:       memoryLimit,
		preset:            parsedPreset,
		includeCandidates: input.includeCandidates,
		asOf:              parsedAsOf,
		staleAfter:        input.staleAfter,
		allowStale:        input.allowStale,
	})
}

type compactSummaryOptions struct {
	sessionID         string
	workspace         string
	recentCount       int
	memoryLimit       int
	preset            apptypes.MemoryRetrievalPreset
	includeCandidates bool
	asOf              types.Optional[time.Time]
	staleAfter        time.Duration
	allowStale        bool
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
			IncludeMemoryCandidates(opts.includeCandidates).
			MemoryAsOf(opts.asOf).
			StaleAfter(opts.staleAfter).
			AllowStale(opts.allowStale).
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
		sb.WriteString("  Run traceary list for full history.\n")
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
	if candidates := pack.MemoryNeedsReview(); len(candidates) > 0 {
		sb.WriteString("  needs_review: ")
		for index, memory := range candidates {
			if index > 0 {
				sb.WriteString(" | ")
			}
			sb.WriteString(truncateCompactSummarySegment(memory.Fact(), 60))
		}
		sb.WriteString("\n")
	} else if pack.CandidateMemoryCount() > 0 {
		fmt.Fprintf(&sb, "  needs_review: %d memory candidates omitted (run context --handoff --include-candidates)\n", pack.CandidateMemoryCount())
	}
	sb.WriteString("  Run traceary list for full history.\n")
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
