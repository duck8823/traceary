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
// `traceary session handoff --compact-only`. It matches the v0.8.x
// `traceary compact-summary` default (removed in v0.14.0) so the
// compact output stays byte-for-byte compatible.
const compactSummaryDefaultRecent = 3

// compactSummaryOptions captures every knob the compact-summary path
// supports. `session handoff --compact-only` threads --memories /
// --preset / --as-of through here so those flags are not silent
// no-ops.
type compactSummaryOptions struct {
	sessionID   string
	workspace   string
	recentCount int
	memoryLimit int
	preset      apptypes.MemoryRetrievalPreset
	asOf        types.Optional[time.Time]
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
