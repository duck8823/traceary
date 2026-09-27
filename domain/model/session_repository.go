package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionRepository persists Session aggregates.
type SessionRepository interface {
	// Save persists a session.
	Save(ctx context.Context, session *Session) error
	// SaveBoundary atomically registers ordinary identity/start boundaries or
	// appends an ordinary end marker without changing legacy lifecycle fields.
	// Supervisor result writes use the separate OneShotSessionRepository port.
	SaveBoundary(ctx context.Context, session *Session, event *Event) error
	// FindByID returns the session for the given ID.
	// Returns an empty Optional when the session does not exist.
	FindByID(ctx context.Context, sessionID types.SessionID) (types.Optional[*Session], error)
	// FindEndedSessionIDs returns the subset of sessionIDs that exist and have ended.
	FindEndedSessionIDs(ctx context.Context, sessionIDs []types.SessionID) (map[types.SessionID]struct{}, error)
	// NextChildSpawnOrder returns the next sibling order for a child session
	// under the given parent.
	NextChildSpawnOrder(ctx context.Context, parentSessionID types.SessionID) (int, error)
	// UpdateModelIfEmpty writes model into sessions.model when the existing
	// value is empty. Host-reported values are never overwritten. Returns true
	// when a row was updated.
	UpdateModelIfEmpty(ctx context.Context, sessionID types.SessionID, model string) (bool, error)
	// FindOpenChildSessionIDs returns the IDs of direct children of
	// parentSessionID that have not yet ended.
	FindOpenChildSessionIDs(ctx context.Context, parentSessionID types.SessionID) ([]types.SessionID, error)
}

// OneShotSessionRepository is the supervisor-only outcome persistence port.
// Ordinary SessionRepository boundary writes cannot finalize a stored one-shot.
type OneShotSessionRepository interface {
	SaveOneShotBoundary(ctx context.Context, session *Session, event *Event) error
}
