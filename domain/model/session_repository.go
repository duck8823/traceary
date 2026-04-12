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
}
