package types

import (
	"slices"
	"time"
)

// TimelineWorkspaceBreakdownSummarySource identifies which event kind the
// workspace activity summary was derived from. Consumers can disambiguate
// between user-intent (prompt), agent-report (compact_summary), and the
// kind-count fallback.
type TimelineWorkspaceBreakdownSummarySource string

const (
	// TimelineSummarySourceCompactSummary means the summary came from a
	// compact_summary event body recorded for this workspace in the block.
	TimelineSummarySourceCompactSummary TimelineWorkspaceBreakdownSummarySource = "compact_summary"
	// TimelineSummarySourcePrompt means the summary came from the first
	// prompt event body recorded for this workspace in the block.
	TimelineSummarySourcePrompt TimelineWorkspaceBreakdownSummarySource = "prompt"
	// TimelineSummarySourceTranscript means the summary came from the
	// first transcript event body recorded for this workspace in the
	// block (assistant-reasoning signal, introduced with #606).
	TimelineSummarySourceTranscript TimelineWorkspaceBreakdownSummarySource = "transcript"
	// TimelineSummarySourceKindCounts means no summary event was available
	// and the renderer should fall back to the kind-count line.
	TimelineSummarySourceKindCounts TimelineWorkspaceBreakdownSummarySource = "kind_counts"
)

// TimelineWorkspaceBreakdown captures per-workspace activity inside a
// timeline block. It is always nested under a TimelineBlock.
type TimelineWorkspaceBreakdown struct {
	workspace     string
	eventCount    int
	kinds         []string
	summary       string
	summarySource TimelineWorkspaceBreakdownSummarySource
}

// TimelineWorkspaceBreakdownOf creates a TimelineWorkspaceBreakdown.
func TimelineWorkspaceBreakdownOf(
	workspace string,
	eventCount int,
	kinds []string,
	summary string,
	summarySource TimelineWorkspaceBreakdownSummarySource,
) TimelineWorkspaceBreakdown {
	return TimelineWorkspaceBreakdown{
		workspace:     workspace,
		eventCount:    eventCount,
		kinds:         slices.Clone(kinds),
		summary:       summary,
		summarySource: summarySource,
	}
}

// Workspace returns the workspace identifier.
func (b TimelineWorkspaceBreakdown) Workspace() string { return b.workspace }

// EventCount returns the number of events attributed to this workspace.
func (b TimelineWorkspaceBreakdown) EventCount() int { return b.eventCount }

// Kinds returns the kinds observed for this workspace inside the block.
func (b TimelineWorkspaceBreakdown) Kinds() []string { return slices.Clone(b.kinds) }

// Summary returns the short activity summary for this workspace.
func (b TimelineWorkspaceBreakdown) Summary() string { return b.summary }

// SummarySource returns which event kind the summary was derived from.
func (b TimelineWorkspaceBreakdown) SummarySource() TimelineWorkspaceBreakdownSummarySource {
	return b.summarySource
}

// TimelineBlock represents a contiguous work block separated by idle gaps.
// It holds block-level aggregates plus a per-workspace breakdown.
type TimelineBlock struct {
	blockStart         time.Time
	blockEnd           time.Time
	eventCount         int
	agents             []string
	workspaceBreakdown []TimelineWorkspaceBreakdown
}

// TimelineBlockOf creates a TimelineBlock.
func TimelineBlockOf(
	blockStart time.Time,
	blockEnd time.Time,
	eventCount int,
	agents []string,
	workspaceBreakdown []TimelineWorkspaceBreakdown,
) TimelineBlock {
	return TimelineBlock{
		blockStart:         blockStart,
		blockEnd:           blockEnd,
		eventCount:         eventCount,
		agents:             slices.Clone(agents),
		workspaceBreakdown: slices.Clone(workspaceBreakdown),
	}
}

// BlockStart returns when the block started.
func (b TimelineBlock) BlockStart() time.Time { return b.blockStart }

// BlockEnd returns when the block ended.
func (b TimelineBlock) BlockEnd() time.Time { return b.blockEnd }

// EventCount returns the number of events in the block.
func (b TimelineBlock) EventCount() int { return b.eventCount }

// Agents returns the agents involved.
func (b TimelineBlock) Agents() []string { return slices.Clone(b.agents) }

// WorkspaceBreakdown returns per-workspace activity inside the block.
func (b TimelineBlock) WorkspaceBreakdown() []TimelineWorkspaceBreakdown {
	return slices.Clone(b.workspaceBreakdown)
}

// Workspaces returns the distinct workspaces involved in the block, derived
// from the workspace breakdown for backward compatibility with callers that
// only need the workspace list.
func (b TimelineBlock) Workspaces() []string {
	out := make([]string, 0, len(b.workspaceBreakdown))
	for _, ws := range b.workspaceBreakdown {
		out = append(out, ws.workspace)
	}
	return out
}

// Kinds returns the union of kinds seen across every workspace in the
// block, preserving each workspace's kind ordering.
func (b TimelineBlock) Kinds() []string {
	var out []string
	for _, ws := range b.workspaceBreakdown {
		out = append(out, ws.kinds...)
	}
	return out
}
