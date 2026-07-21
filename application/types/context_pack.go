package types

import (
	"slices"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// ContextPack is the structured working-memory bundle shared by CLI and MCP
// handoff surfaces.
type ContextPack struct {
	sessionID            domtypes.SessionID
	workspace            domtypes.Workspace
	requestedWorkspace   domtypes.Workspace
	label                string
	status               string
	totalEvents          int
	commandCount         int
	agents               []string
	workingState         WorkingState
	recentCommands       []string
	recentCommandItems   []RecentCommandSummary
	memories             []MemorySummary
	memoryNeedsReview    []MemorySummary
	acceptedMemoryCount  int
	candidateMemoryCount int
}

// WithRecentCommandItems returns a copy with structured recent-command
// projections. The legacy RecentCommands list remains unchanged.
func (c ContextPack) WithRecentCommandItems(items []RecentCommandSummary) ContextPack {
	c.recentCommandItems = slices.Clone(items)
	return c
}

// ContextPackOf creates a ContextPack.
func ContextPackOf(
	sessionID domtypes.SessionID,
	workspace domtypes.Workspace,
	label string,
	status string,
	totalEvents int,
	commandCount int,
	agents []string,
	workingState WorkingState,
	recentCommands []string,
	memories []MemorySummary,
) ContextPack {
	trustedMemories, reviewMemories := splitContextPackMemories(memories)
	return ContextPack{
		sessionID:            sessionID,
		workspace:            workspace,
		label:                label,
		status:               status,
		totalEvents:          totalEvents,
		commandCount:         commandCount,
		agents:               slices.Clone(agents),
		workingState:         workingState,
		recentCommands:       slices.Clone(recentCommands),
		memories:             trustedMemories,
		memoryNeedsReview:    reviewMemories,
		acceptedMemoryCount:  len(trustedMemories),
		candidateMemoryCount: countMemoriesByStatus(reviewMemories, domtypes.MemoryStatusCandidate),
	}
}

// WithRequestedWorkspace returns a copy of the pack with the originally
// requested workspace recorded. When the requested value differs from the
// matched session workspace, callers can surface a parent-fallback hint via
// WorkspaceFallbackUsed.
func (c ContextPack) WithRequestedWorkspace(requested domtypes.Workspace) ContextPack {
	c.requestedWorkspace = requested
	return c
}

// RequestedWorkspace returns the workspace the caller asked for, which may
// differ from the matched session workspace when parent fallback was applied.
// Returns an empty workspace when the caller did not request any specific
// workspace.
func (c ContextPack) RequestedWorkspace() domtypes.Workspace { return c.requestedWorkspace }

// WorkspaceFallbackUsed reports whether the pack was assembled by walking up
// to a parent workspace because no session existed under the requested one.
func (c ContextPack) WorkspaceFallbackUsed() bool {
	if c.requestedWorkspace.String() == "" {
		return false
	}
	return c.requestedWorkspace != c.workspace
}

// SessionID returns the session ID.
func (c ContextPack) SessionID() domtypes.SessionID { return c.sessionID }

// Workspace returns the workspace.
func (c ContextPack) Workspace() domtypes.Workspace { return c.workspace }

// Label returns the session label.
func (c ContextPack) Label() string { return c.label }

// Status returns the session status.
func (c ContextPack) Status() string { return c.status }

// TotalEvents returns the total number of events in the session.
func (c ContextPack) TotalEvents() int { return c.totalEvents }

// CommandCount returns the total number of command events in the session.
func (c ContextPack) CommandCount() int { return c.commandCount }

// Agents returns the participating agents.
func (c ContextPack) Agents() []string { return slices.Clone(c.agents) }

// WorkingState returns the structured working-memory state.
func (c ContextPack) WorkingState() WorkingState { return c.workingState }

// RecentCommands returns recent command summaries.
func (c ContextPack) RecentCommands() []string { return slices.Clone(c.recentCommands) }

// RecentCommandItems returns structured recent-command projections.
func (c ContextPack) RecentCommandItems() []RecentCommandSummary {
	return slices.Clone(c.recentCommandItems)
}

// Memories returns accepted durable memories relevant to the pack.
func (c ContextPack) Memories() []MemorySummary { return slices.Clone(c.memories) }

// MemoryNeedsReview returns candidate durable memories that were
// explicitly included for review. They are intentionally separate from
// Memories so host contexts do not treat unaccepted facts as trusted.
func (c ContextPack) MemoryNeedsReview() []MemorySummary {
	return slices.Clone(c.memoryNeedsReview)
}

// AcceptedMemoryCount returns the number of trusted accepted memories
// represented by the pack. For query-built packs this count reflects
// the accepted rows loaded under the context-pack limit.
func (c ContextPack) AcceptedMemoryCount() int { return c.acceptedMemoryCount }

// CandidateMemoryCount returns the number of candidate rows observed
// under the context-pack limit. Candidate facts are only included in
// MemoryNeedsReview when IncludeMemoryCandidates was requested.
func (c ContextPack) CandidateMemoryCount() int { return c.candidateMemoryCount }

// WithMemoryNeedsReview returns a copy of the pack with the candidate
// review section and candidate count populated.
func (c ContextPack) WithMemoryNeedsReview(candidates []MemorySummary, candidateCount int) ContextPack {
	c.memoryNeedsReview = slices.Clone(candidates)
	c.candidateMemoryCount = candidateCount
	return c
}

// WithMemoryCounts returns a copy of the pack with explicit trust
// counters. This is used by query-built packs to expose accepted and
// candidate counts even when candidates are intentionally omitted.
func (c ContextPack) WithMemoryCounts(acceptedCount int, candidateCount int) ContextPack {
	c.acceptedMemoryCount = acceptedCount
	c.candidateMemoryCount = candidateCount
	return c
}

func countMemoriesByStatus(memories []MemorySummary, status domtypes.MemoryStatus) int {
	count := 0
	for _, memory := range memories {
		if memory.Status() == status {
			count++
		}
	}
	return count
}

func splitContextPackMemories(memories []MemorySummary) ([]MemorySummary, []MemorySummary) {
	trusted := make([]MemorySummary, 0, len(memories))
	needsReview := make([]MemorySummary, 0)
	for _, memory := range memories {
		if memory.Status() == domtypes.MemoryStatusAccepted {
			trusted = append(trusted, memory)
			continue
		}
		needsReview = append(needsReview, memory)
	}
	return trusted, needsReview
}
