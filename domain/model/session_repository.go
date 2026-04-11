package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionRepository defines persistence operations for the Session aggregate.
type SessionRepository interface {
	// Save persists a Session (insert or update).
	Save(ctx context.Context, session *Session) error

	// GetByID retrieves a Session by its session ID.
	GetByID(ctx context.Context, sessionID types.SessionID) (*Session, error)
}
