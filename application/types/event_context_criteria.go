package types

import (
	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventContextCriteria holds filter parameters for context event retrieval.
// Zero-value fields are ignored (no filter applied).
type EventContextCriteria struct {
	workspace domtypes.Workspace
	sessionID domtypes.SessionID
	client    domtypes.Client
	agent     domtypes.Agent
	limit     int
}

// Workspace returns the workspace filter.
func (c EventContextCriteria) Workspace() domtypes.Workspace { return c.workspace }

// SessionID returns the session ID filter.
func (c EventContextCriteria) SessionID() domtypes.SessionID { return c.sessionID }

// Client returns the client filter.
func (c EventContextCriteria) Client() domtypes.Client { return c.client }

// Agent returns the agent filter.
func (c EventContextCriteria) Agent() domtypes.Agent { return c.agent }

// Limit returns the maximum number of events to return.
func (c EventContextCriteria) Limit() int { return c.limit }

// EventContextCriteriaBuilder builds an EventContextCriteria value.
type EventContextCriteriaBuilder struct {
	criteria EventContextCriteria
}

// NewEventContextCriteriaBuilder starts building with the given limit.
// Limit is required; other fields are optional.
func NewEventContextCriteriaBuilder(limit int) *EventContextCriteriaBuilder {
	return &EventContextCriteriaBuilder{criteria: EventContextCriteria{limit: limit}}
}

// Workspace sets the workspace filter.
func (b *EventContextCriteriaBuilder) Workspace(workspace domtypes.Workspace) *EventContextCriteriaBuilder {
	b.criteria.workspace = workspace
	return b
}

// SessionID sets the session ID filter.
func (b *EventContextCriteriaBuilder) SessionID(sessionID domtypes.SessionID) *EventContextCriteriaBuilder {
	b.criteria.sessionID = sessionID
	return b
}

// Client sets the client filter.
func (b *EventContextCriteriaBuilder) Client(client domtypes.Client) *EventContextCriteriaBuilder {
	b.criteria.client = client
	return b
}

// Agent sets the agent filter.
func (b *EventContextCriteriaBuilder) Agent(agent domtypes.Agent) *EventContextCriteriaBuilder {
	b.criteria.agent = agent
	return b
}

// Build finalizes and returns the EventContextCriteria.
func (b *EventContextCriteriaBuilder) Build() EventContextCriteria {
	return b.criteria
}
