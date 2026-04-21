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
	memoryAsOf          domtypes.Optional[time.Time]
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

// MemoryAsOf returns the point-in-time at which content validity should
// be evaluated when loading durable memories for the pack. A present
// value lets an operator time-travel handoff / memory_pack to "what
// was valid at time T" instead of "what is valid now". None (the
// default) evaluates validity against the current time.
func (c ContextPackCriteria) MemoryAsOf() domtypes.Optional[time.Time] { return c.memoryAsOf }

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

// MemoryAsOf sets the as-of timestamp used when filtering memory
// validity windows. Callers pass domtypes.None[time.Time]() to clear
// any previously-set value and evaluate validity against "now".
func (b *ContextPackCriteriaBuilder) MemoryAsOf(asOf domtypes.Optional[time.Time]) *ContextPackCriteriaBuilder {
	b.criteria.memoryAsOf = asOf
	return b
}

// Build finalizes and returns the criteria.
func (b *ContextPackCriteriaBuilder) Build() ContextPackCriteria {
	return b.criteria
}
