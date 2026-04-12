package types

import (
	domtypes "github.com/duck8823/traceary/domain/types"
)

// SessionLookupCriteria holds filter parameters for single-session lookup
// (active session, latest session).
// Zero-value fields are ignored (no filter applied).
type SessionLookupCriteria struct {
	client     domtypes.Client
	agent      domtypes.Agent
	workspace  domtypes.Workspace
	activeOnly bool
}

// Client returns the client filter.
func (c SessionLookupCriteria) Client() domtypes.Client { return c.client }

// Agent returns the agent filter.
func (c SessionLookupCriteria) Agent() domtypes.Agent { return c.agent }

// Workspace returns the workspace filter.
func (c SessionLookupCriteria) Workspace() domtypes.Workspace { return c.workspace }

// ActiveOnly reports whether only active sessions should be considered.
func (c SessionLookupCriteria) ActiveOnly() bool { return c.activeOnly }

// SessionLookupCriteriaBuilder builds a SessionLookupCriteria value.
type SessionLookupCriteriaBuilder struct {
	criteria SessionLookupCriteria
}

// NewSessionLookupCriteriaBuilder starts building an empty SessionLookupCriteria.
func NewSessionLookupCriteriaBuilder() *SessionLookupCriteriaBuilder {
	return &SessionLookupCriteriaBuilder{}
}

// Client sets the client filter.
func (b *SessionLookupCriteriaBuilder) Client(client domtypes.Client) *SessionLookupCriteriaBuilder {
	b.criteria.client = client
	return b
}

// Agent sets the agent filter.
func (b *SessionLookupCriteriaBuilder) Agent(agent domtypes.Agent) *SessionLookupCriteriaBuilder {
	b.criteria.agent = agent
	return b
}

// Workspace sets the workspace filter.
func (b *SessionLookupCriteriaBuilder) Workspace(workspace domtypes.Workspace) *SessionLookupCriteriaBuilder {
	b.criteria.workspace = workspace
	return b
}

// ActiveOnly restricts the lookup to active sessions when set to true.
func (b *SessionLookupCriteriaBuilder) ActiveOnly(activeOnly bool) *SessionLookupCriteriaBuilder {
	b.criteria.activeOnly = activeOnly
	return b
}

// Build finalizes and returns the SessionLookupCriteria.
func (b *SessionLookupCriteriaBuilder) Build() SessionLookupCriteria {
	return b.criteria
}
