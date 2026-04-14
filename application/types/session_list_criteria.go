package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// SessionListCriteria holds filter parameters for session listing.
// Zero-value fields are ignored (no filter applied).
type SessionListCriteria struct {
	limit     int
	offset    int
	sessionID domtypes.SessionID
	workspace domtypes.Workspace
	client    domtypes.Client
	agent     domtypes.Agent
	label     string
	from      domtypes.Optional[time.Time]
	to        domtypes.Optional[time.Time]
}

// Limit returns the maximum number of results to return.
func (c SessionListCriteria) Limit() int { return c.limit }

// Offset returns the number of results to skip before returning matches.
func (c SessionListCriteria) Offset() int { return c.offset }

// SessionID returns the session ID filter.
func (c SessionListCriteria) SessionID() domtypes.SessionID { return c.sessionID }

// Workspace returns the workspace filter.
func (c SessionListCriteria) Workspace() domtypes.Workspace { return c.workspace }

// Client returns the client filter.
func (c SessionListCriteria) Client() domtypes.Client { return c.client }

// Agent returns the agent filter.
func (c SessionListCriteria) Agent() domtypes.Agent { return c.agent }

// Label returns the session label filter.
func (c SessionListCriteria) Label() string { return c.label }

// From returns the lower bound of the time range (inclusive).
func (c SessionListCriteria) From() domtypes.Optional[time.Time] { return c.from }

// To returns the upper bound of the time range (exclusive).
func (c SessionListCriteria) To() domtypes.Optional[time.Time] { return c.to }

// SessionListCriteriaBuilder builds a SessionListCriteria value.
type SessionListCriteriaBuilder struct {
	criteria SessionListCriteria
}

// NewSessionListCriteriaBuilder starts building with the given limit.
// Limit is required; other fields are optional.
func NewSessionListCriteriaBuilder(limit int) *SessionListCriteriaBuilder {
	return &SessionListCriteriaBuilder{
		criteria: SessionListCriteria{
			limit: limit,
			from:  domtypes.None[time.Time](),
			to:    domtypes.None[time.Time](),
		},
	}
}

// Offset sets the number of results to skip before returning matches.
func (b *SessionListCriteriaBuilder) Offset(offset int) *SessionListCriteriaBuilder {
	b.criteria.offset = offset
	return b
}

// SessionID sets the session ID filter.
func (b *SessionListCriteriaBuilder) SessionID(sessionID domtypes.SessionID) *SessionListCriteriaBuilder {
	b.criteria.sessionID = sessionID
	return b
}

// Workspace sets the workspace filter.
func (b *SessionListCriteriaBuilder) Workspace(workspace domtypes.Workspace) *SessionListCriteriaBuilder {
	b.criteria.workspace = workspace
	return b
}

// Client sets the client filter.
func (b *SessionListCriteriaBuilder) Client(client domtypes.Client) *SessionListCriteriaBuilder {
	b.criteria.client = client
	return b
}

// Agent sets the agent filter.
func (b *SessionListCriteriaBuilder) Agent(agent domtypes.Agent) *SessionListCriteriaBuilder {
	b.criteria.agent = agent
	return b
}

// Label sets the session label filter.
func (b *SessionListCriteriaBuilder) Label(label string) *SessionListCriteriaBuilder {
	b.criteria.label = label
	return b
}

// From sets the lower bound of the time range (inclusive).
func (b *SessionListCriteriaBuilder) From(from domtypes.Optional[time.Time]) *SessionListCriteriaBuilder {
	b.criteria.from = from
	return b
}

// To sets the upper bound of the time range (exclusive).
func (b *SessionListCriteriaBuilder) To(to domtypes.Optional[time.Time]) *SessionListCriteriaBuilder {
	b.criteria.to = to
	return b
}

// Build finalizes and returns the SessionListCriteria.
func (b *SessionListCriteriaBuilder) Build() SessionListCriteria {
	return b.criteria
}
