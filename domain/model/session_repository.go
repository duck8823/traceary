package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionRepository persists Session aggregates.
type SessionRepository interface {
	// SaveSession persists a session.
	SaveSession(ctx context.Context, session *Session) error
	// FindByID returns the session for the given ID.
	// Returns an empty Optional when the session does not exist.
	FindByID(ctx context.Context, sessionID types.SessionID) (types.Optional[*Session], error)
}
