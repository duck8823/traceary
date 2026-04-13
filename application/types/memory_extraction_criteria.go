package types

import domtypes "github.com/duck8823/traceary/domain/types"

const (
	defaultMemoryExtractionEventLimit     = 5
	defaultMemoryExtractionCandidateLimit = 10
)

// MemoryExtractionCriteria describes how to extract candidate durable memories
// from existing session/history signals.
type MemoryExtractionCriteria struct {
	sessionID      domtypes.SessionID
	workspace      domtypes.Workspace
	eventLimit     int
	candidateLimit int
}

// SessionID returns the target session ID.
func (c MemoryExtractionCriteria) SessionID() domtypes.SessionID { return c.sessionID }

// Workspace returns the target workspace.
func (c MemoryExtractionCriteria) Workspace() domtypes.Workspace { return c.workspace }

// EventLimit returns the maximum number of recent events to inspect per kind.
func (c MemoryExtractionCriteria) EventLimit() int { return c.eventLimit }

// CandidateLimit returns the maximum number of candidates to persist.
func (c MemoryExtractionCriteria) CandidateLimit() int { return c.candidateLimit }

// MemoryExtractionCriteriaBuilder builds MemoryExtractionCriteria values.
type MemoryExtractionCriteriaBuilder struct {
	criteria MemoryExtractionCriteria
}

// NewMemoryExtractionCriteriaBuilder starts a criteria builder with sensible
// defaults.
func NewMemoryExtractionCriteriaBuilder() *MemoryExtractionCriteriaBuilder {
	return &MemoryExtractionCriteriaBuilder{criteria: MemoryExtractionCriteria{
		eventLimit:     defaultMemoryExtractionEventLimit,
		candidateLimit: defaultMemoryExtractionCandidateLimit,
	}}
}

// SessionID sets the target session ID.
func (b *MemoryExtractionCriteriaBuilder) SessionID(sessionID domtypes.SessionID) *MemoryExtractionCriteriaBuilder {
	b.criteria.sessionID = sessionID
	return b
}

// Workspace sets the target workspace.
func (b *MemoryExtractionCriteriaBuilder) Workspace(workspace domtypes.Workspace) *MemoryExtractionCriteriaBuilder {
	b.criteria.workspace = workspace
	return b
}

// EventLimit sets the per-kind event inspection limit.
func (b *MemoryExtractionCriteriaBuilder) EventLimit(limit int) *MemoryExtractionCriteriaBuilder {
	b.criteria.eventLimit = limit
	return b
}

// CandidateLimit sets the maximum number of extracted candidates.
func (b *MemoryExtractionCriteriaBuilder) CandidateLimit(limit int) *MemoryExtractionCriteriaBuilder {
	b.criteria.candidateLimit = limit
	return b
}

// Build finalizes the criteria.
func (b *MemoryExtractionCriteriaBuilder) Build() MemoryExtractionCriteria {
	return b.criteria
}
