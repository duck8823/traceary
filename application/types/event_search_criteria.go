package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventSearchCriteria holds filter parameters for full-text event search.
// Zero-value fields are ignored (no filter applied).
type EventSearchCriteria struct {
	query        string
	workspace    domtypes.Workspace
	sessionID    domtypes.SessionID
	client       domtypes.Client
	agent        domtypes.Agent
	kind         domtypes.EventKind
	from         time.Time
	to           time.Time
	limit        int
	offset       int
	failuresOnly bool
}

// Query returns the search query string.
func (c EventSearchCriteria) Query() string { return c.query }

// Workspace returns the workspace filter.
func (c EventSearchCriteria) Workspace() domtypes.Workspace { return c.workspace }

// SessionID returns the session ID filter.
func (c EventSearchCriteria) SessionID() domtypes.SessionID { return c.sessionID }

// Client returns the client filter.
func (c EventSearchCriteria) Client() domtypes.Client { return c.client }

// Agent returns the agent filter.
func (c EventSearchCriteria) Agent() domtypes.Agent { return c.agent }

// Kind returns the event kind filter.
func (c EventSearchCriteria) Kind() domtypes.EventKind { return c.kind }

// From returns the lower bound of the time range (inclusive).
func (c EventSearchCriteria) From() time.Time { return c.from }

// To returns the upper bound of the time range (exclusive).
func (c EventSearchCriteria) To() time.Time { return c.to }

// Limit returns the maximum number of results to return.
func (c EventSearchCriteria) Limit() int { return c.limit }

// Offset returns the number of results to skip before returning matches.
func (c EventSearchCriteria) Offset() int { return c.offset }

// FailuresOnly reports whether only failed commands should be returned.
func (c EventSearchCriteria) FailuresOnly() bool { return c.failuresOnly }

// EventSearchCriteriaBuilder builds an EventSearchCriteria value.
type EventSearchCriteriaBuilder struct {
	criteria EventSearchCriteria
}

// NewEventSearchCriteriaBuilder starts building with the given limit.
// Limit is required; other fields are optional.
func NewEventSearchCriteriaBuilder(limit int) *EventSearchCriteriaBuilder {
	return &EventSearchCriteriaBuilder{criteria: EventSearchCriteria{limit: limit}}
}

// Query sets the search query string.
func (b *EventSearchCriteriaBuilder) Query(query string) *EventSearchCriteriaBuilder {
	b.criteria.query = query
	return b
}

// Workspace sets the workspace filter.
func (b *EventSearchCriteriaBuilder) Workspace(workspace domtypes.Workspace) *EventSearchCriteriaBuilder {
	b.criteria.workspace = workspace
	return b
}

// SessionID sets the session ID filter.
func (b *EventSearchCriteriaBuilder) SessionID(sessionID domtypes.SessionID) *EventSearchCriteriaBuilder {
	b.criteria.sessionID = sessionID
	return b
}

// Client sets the client filter.
func (b *EventSearchCriteriaBuilder) Client(client domtypes.Client) *EventSearchCriteriaBuilder {
	b.criteria.client = client
	return b
}

// Agent sets the agent filter.
func (b *EventSearchCriteriaBuilder) Agent(agent domtypes.Agent) *EventSearchCriteriaBuilder {
	b.criteria.agent = agent
	return b
}

// Kind sets the event kind filter.
func (b *EventSearchCriteriaBuilder) Kind(kind domtypes.EventKind) *EventSearchCriteriaBuilder {
	b.criteria.kind = kind
	return b
}

// From sets the lower bound of the time range (inclusive).
func (b *EventSearchCriteriaBuilder) From(from time.Time) *EventSearchCriteriaBuilder {
	b.criteria.from = from
	return b
}

// To sets the upper bound of the time range (exclusive).
func (b *EventSearchCriteriaBuilder) To(to time.Time) *EventSearchCriteriaBuilder {
	b.criteria.to = to
	return b
}

// Offset sets the number of results to skip before returning matches.
func (b *EventSearchCriteriaBuilder) Offset(offset int) *EventSearchCriteriaBuilder {
	b.criteria.offset = offset
	return b
}

// FailuresOnly restricts results to failed commands when set to true.
func (b *EventSearchCriteriaBuilder) FailuresOnly(failuresOnly bool) *EventSearchCriteriaBuilder {
	b.criteria.failuresOnly = failuresOnly
	return b
}

// Build finalizes and returns the EventSearchCriteria.
func (b *EventSearchCriteriaBuilder) Build() EventSearchCriteria {
	return b.criteria
}
