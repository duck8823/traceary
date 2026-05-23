package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	defaultContextPackRecentCommandsLimit = 5
	defaultContextPackMemoryLimit         = 5
)

// ContextPackCriteria describes how to assemble a structured handoff pack.
type ContextPackCriteria struct {
	sessionID           domtypes.SessionID
	workspace           domtypes.Workspace
	recentCommandsLimit int
	memoryLimit         int
	memoryPreset        MemoryRetrievalPreset
	includeCandidates   bool
	memoryAsOf          domtypes.Optional[time.Time]
	allowStale          bool
	staleAfter          time.Duration
}

// SessionID returns the target session filter.
func (c ContextPackCriteria) SessionID() domtypes.SessionID { return c.sessionID }

// Workspace returns the target workspace filter.
func (c ContextPackCriteria) Workspace() domtypes.Workspace { return c.workspace }

// RecentCommandsLimit returns the maximum number of recent commands to include.
func (c ContextPackCriteria) RecentCommandsLimit() int { return c.recentCommandsLimit }

// MemoryLimit returns the maximum number of durable memories to include.
func (c ContextPackCriteria) MemoryLimit() int { return c.memoryLimit }

// MemoryPreset returns the retrieval preset to apply when loading
// durable memories for the pack. Empty means "no preset — use the
// default accepted-only behavior of the memory query path."
func (c ContextPackCriteria) MemoryPreset() MemoryRetrievalPreset { return c.memoryPreset }

// IncludeMemoryCandidates reports whether candidate durable memories
// should be included in the review-oriented section of a context pack.
// Defaults to false so handoff / MCP context treat only accepted
// durable memories as trusted.
func (c ContextPackCriteria) IncludeMemoryCandidates() bool { return c.includeCandidates }

// MemoryAsOf returns the point-in-time at which content validity should
// be evaluated when loading durable memories for the pack. A present
// value lets an operator time-travel handoff / memory_pack to "what
// was valid at time T" instead of "what is valid now". None (the
// default) evaluates validity against the current time.
func (c ContextPackCriteria) MemoryAsOf() domtypes.Optional[time.Time] { return c.memoryAsOf }

// AllowStale reports whether stale active sessions are eligible for
// selection. Defaults to false so the handoff does not silently surface
// an abandoned session as the current working context.
func (c ContextPackCriteria) AllowStale() bool { return c.allowStale }

// StaleAfter returns the duration after which an unended session is
// treated as stale. A zero or negative value disables the stale check
// (matching the historical behavior of the builder).
func (c ContextPackCriteria) StaleAfter() time.Duration { return c.staleAfter }

// ContextPackCriteriaBuilder builds a ContextPackCriteria value.
type ContextPackCriteriaBuilder struct {
	criteria ContextPackCriteria
}

// NewContextPackCriteriaBuilder starts building a ContextPackCriteria with
// sensible defaults for command and memory limits.
func NewContextPackCriteriaBuilder() *ContextPackCriteriaBuilder {
	return &ContextPackCriteriaBuilder{criteria: ContextPackCriteria{
		recentCommandsLimit: defaultContextPackRecentCommandsLimit,
		memoryLimit:         defaultContextPackMemoryLimit,
	}}
}

// SessionID sets the target session ID.
func (b *ContextPackCriteriaBuilder) SessionID(sessionID domtypes.SessionID) *ContextPackCriteriaBuilder {
	b.criteria.sessionID = sessionID
	return b
}

// Workspace sets the target workspace.
func (b *ContextPackCriteriaBuilder) Workspace(workspace domtypes.Workspace) *ContextPackCriteriaBuilder {
	b.criteria.workspace = workspace
	return b
}

// RecentCommandsLimit sets the recent command limit.
func (b *ContextPackCriteriaBuilder) RecentCommandsLimit(limit int) *ContextPackCriteriaBuilder {
	b.criteria.recentCommandsLimit = limit
	return b
}

// MemoryLimit sets the durable memory limit.
func (b *ContextPackCriteriaBuilder) MemoryLimit(limit int) *ContextPackCriteriaBuilder {
	b.criteria.memoryLimit = limit
	return b
}

// MemoryPreset sets the retrieval preset used to pre-populate the
// durable-memory filters for the pack.
func (b *ContextPackCriteriaBuilder) MemoryPreset(preset MemoryRetrievalPreset) *ContextPackCriteriaBuilder {
	b.criteria.memoryPreset = preset
	return b
}

// IncludeMemoryCandidates opts in to including candidate memories in a
// separate review-oriented section. Candidate backlog counts can still
// be populated when this is false, but candidate facts are not mixed
// into the trusted memory section.
func (b *ContextPackCriteriaBuilder) IncludeMemoryCandidates(include bool) *ContextPackCriteriaBuilder {
	b.criteria.includeCandidates = include
	return b
}

// MemoryAsOf sets the as-of timestamp used when filtering memory
// validity windows. Callers pass domtypes.None[time.Time]() to clear
// any previously-set value and evaluate validity against "now".
func (b *ContextPackCriteriaBuilder) MemoryAsOf(asOf domtypes.Optional[time.Time]) *ContextPackCriteriaBuilder {
	b.criteria.memoryAsOf = asOf
	return b
}

// AllowStale opts the caller in to stale active sessions. When false
// (the default), the builder skips a session whose start is older than
// StaleAfter so the handoff does not silently surface an abandoned
// session as the current working context.
func (b *ContextPackCriteriaBuilder) AllowStale(allow bool) *ContextPackCriteriaBuilder {
	b.criteria.allowStale = allow
	return b
}

// StaleAfter sets the duration after which an unended session is
// treated as stale. A zero or negative value disables the stale check.
func (b *ContextPackCriteriaBuilder) StaleAfter(staleAfter time.Duration) *ContextPackCriteriaBuilder {
	b.criteria.staleAfter = staleAfter
	return b
}

// Build finalizes and returns the criteria.
func (b *ContextPackCriteriaBuilder) Build() ContextPackCriteria {
	return b.criteria
}
