package model

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// ErrSessionStartedEventNotFound indicates the target session has no start event.
var ErrSessionStartedEventNotFound = xerrors.New("session_started event was not found for the target session")

// SessionRepository persists Session aggregates.
type SessionRepository interface {
	// SaveSession persists a session.
	SaveSession(ctx context.Context, session *Session) error
	// FindByID returns the session for the given ID.
	FindByID(ctx context.Context, sessionID types.SessionID) (*Session, error)
}
