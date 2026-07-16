package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionRepository persists Session aggregates.
type SessionRepository interface {
	// Save persists a session.
	Save(ctx context.Context, session *Session) error
	// SaveBoundary atomically persists a session aggregate together with its
	// boundary event (session_started or session_ended). Both writes commit
	// or fail as a single transaction.
	SaveBoundary(ctx context.Context, session *Session, event *Event) error
	// FindByID returns the session for the given ID.
	// Returns an empty Optional when the session does not exist.
	FindByID(ctx context.Context, sessionID types.SessionID) (types.Optional[*Session], error)
	// FindEndedSessionIDs returns the subset of sessionIDs that exist and have ended.
	FindEndedSessionIDs(ctx context.Context, sessionIDs []types.SessionID) (map[types.SessionID]struct{}, error)
	// NextChildSpawnOrder returns the next sibling order for a child session
	// under the given parent.
	NextChildSpawnOrder(ctx context.Context, parentSessionID types.SessionID) (int, error)
	// UpdateSummaryIfEmpty writes summary into sessions.summary when the
	// existing value is NULL or empty, leaving manually authored summaries
	// untouched. Returns true when a row was updated.
	UpdateSummaryIfEmpty(ctx context.Context, sessionID types.SessionID, summary string) (bool, error)
	// UpdateModelIfEmpty writes model into sessions.model when the existing
	// value is empty. Host-reported values are never overwritten. Returns true
	// when a row was updated.
	UpdateModelIfEmpty(ctx context.Context, sessionID types.SessionID, model string) (bool, error)
}
