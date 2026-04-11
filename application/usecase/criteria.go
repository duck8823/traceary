package usecase

import (
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// EventSearchCriteria holds filter parameters for full-text event search.
// Zero-value fields are ignored (no filter applied).
type EventSearchCriteria struct {
	Query        string
	Workspace    types.Workspace
	SessionID    types.SessionID
	Client       types.Client
	Agent        types.Agent
	Kind         types.EventKind
	From         time.Time
	To           time.Time
	Limit        int
	Offset       int
	FailuresOnly bool
}

// EventListCriteria holds filter parameters for event listing.
// Zero-value fields are ignored (no filter applied).
type EventListCriteria struct {
	Limit        int
	Offset       int
	Kind         types.EventKind
	Client       types.Client
	Agent        types.Agent
	SessionID    types.SessionID
	Workspace    types.Workspace
	FailuresOnly bool
	From         time.Time
	To           time.Time
}

// EventContextCriteria holds filter parameters for context event retrieval.
// Zero-value fields are ignored (no filter applied).
type EventContextCriteria struct {
	Workspace types.Workspace
	SessionID types.SessionID
	Client    types.Client
	Agent     types.Agent
	Limit     int
}

// SessionListCriteria holds filter parameters for session listing.
// Zero-value fields are ignored (no filter applied).
type SessionListCriteria struct {
	Limit     int
	Offset    int
	SessionID types.SessionID
	Workspace types.Workspace
	Client    types.Client
	Agent     types.Agent
	Label     string
	From      *time.Time
	To        *time.Time
}

// SessionLookupCriteria holds filter parameters for single-session lookup
// (active session, latest session).
// Zero-value fields are ignored (no filter applied).
type SessionLookupCriteria struct {
	Client     types.Client
	Agent      types.Agent
	Workspace  types.Workspace
	ActiveOnly bool
}

// TimelineCriteria holds filter parameters for work timeline block listing.
// Zero-value fields are ignored (no filter applied).
type TimelineCriteria struct {
	Workspace  types.Workspace
	From       time.Time
	To         time.Time
	GapSeconds int
	Limit      int
}
