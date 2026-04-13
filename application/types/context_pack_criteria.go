package types

import domtypes "github.com/duck8823/traceary/domain/types"

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
}

// SessionID returns the target session filter.
func (c ContextPackCriteria) SessionID() domtypes.SessionID { return c.sessionID }

// Workspace returns the target workspace filter.
func (c ContextPackCriteria) Workspace() domtypes.Workspace { return c.workspace }

// RecentCommandsLimit returns the maximum number of recent commands to include.
func (c ContextPackCriteria) RecentCommandsLimit() int { return c.recentCommandsLimit }

// MemoryLimit returns the maximum number of durable memories to include.
func (c ContextPackCriteria) MemoryLimit() int { return c.memoryLimit }

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

// Build finalizes and returns the criteria.
func (b *ContextPackCriteriaBuilder) Build() ContextPackCriteria {
	return b.criteria
}
