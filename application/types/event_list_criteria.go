package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventListCriteria holds filter parameters for event listing.
// Zero-value fields are ignored (no filter applied).
type EventListCriteria struct {
	limit        int
	offset       int
	kind         domtypes.EventKind
	client       domtypes.Client
	agent        domtypes.Agent
	sessionID    domtypes.SessionID
	workspace    domtypes.Workspace
	failuresOnly bool
	from         time.Time
	to           time.Time
}

// Limit returns the maximum number of results to return.
func (c EventListCriteria) Limit() int { return c.limit }

// Offset returns the number of results to skip before returning matches.
func (c EventListCriteria) Offset() int { return c.offset }

// Kind returns the event kind filter.
func (c EventListCriteria) Kind() domtypes.EventKind { return c.kind }

// Client returns the client filter.
func (c EventListCriteria) Client() domtypes.Client { return c.client }

// Agent returns the agent filter.
func (c EventListCriteria) Agent() domtypes.Agent { return c.agent }

// SessionID returns the session ID filter.
func (c EventListCriteria) SessionID() domtypes.SessionID { return c.sessionID }

// Workspace returns the workspace filter.
func (c EventListCriteria) Workspace() domtypes.Workspace { return c.workspace }

// FailuresOnly reports whether only failed commands should be returned.
func (c EventListCriteria) FailuresOnly() bool { return c.failuresOnly }

// From returns the lower bound of the time range (inclusive).
func (c EventListCriteria) From() time.Time { return c.from }

// To returns the upper bound of the time range (exclusive).
func (c EventListCriteria) To() time.Time { return c.to }

// EventListCriteriaBuilder builds an EventListCriteria value.
type EventListCriteriaBuilder struct {
	criteria EventListCriteria
}

// NewEventListCriteriaBuilder starts building with the given limit.
// Limit is required; other fields are optional.
func NewEventListCriteriaBuilder(limit int) *EventListCriteriaBuilder {
	return &EventListCriteriaBuilder{criteria: EventListCriteria{limit: limit}}
}

// Offset sets the number of results to skip before returning matches.
func (b *EventListCriteriaBuilder) Offset(offset int) *EventListCriteriaBuilder {
	b.criteria.offset = offset
	return b
}

// Kind sets the event kind filter.
func (b *EventListCriteriaBuilder) Kind(kind domtypes.EventKind) *EventListCriteriaBuilder {
	b.criteria.kind = kind
	return b
}

// Client sets the client filter.
func (b *EventListCriteriaBuilder) Client(client domtypes.Client) *EventListCriteriaBuilder {
	b.criteria.client = client
	return b
}

// Agent sets the agent filter.
func (b *EventListCriteriaBuilder) Agent(agent domtypes.Agent) *EventListCriteriaBuilder {
	b.criteria.agent = agent
	return b
}

// SessionID sets the session ID filter.
func (b *EventListCriteriaBuilder) SessionID(sessionID domtypes.SessionID) *EventListCriteriaBuilder {
	b.criteria.sessionID = sessionID
	return b
}

// Workspace sets the workspace filter.
func (b *EventListCriteriaBuilder) Workspace(workspace domtypes.Workspace) *EventListCriteriaBuilder {
	b.criteria.workspace = workspace
	return b
}

// FailuresOnly restricts results to failed commands when set to true.
func (b *EventListCriteriaBuilder) FailuresOnly(failuresOnly bool) *EventListCriteriaBuilder {
	b.criteria.failuresOnly = failuresOnly
	return b
}

// From sets the lower bound of the time range (inclusive).
func (b *EventListCriteriaBuilder) From(from time.Time) *EventListCriteriaBuilder {
	b.criteria.from = from
	return b
}

// To sets the upper bound of the time range (exclusive).
func (b *EventListCriteriaBuilder) To(to time.Time) *EventListCriteriaBuilder {
	b.criteria.to = to
	return b
}

// Build finalizes and returns the EventListCriteria.
func (b *EventListCriteriaBuilder) Build() EventListCriteria {
	return b.criteria
}
