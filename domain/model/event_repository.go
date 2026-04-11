package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// EventRepository defines persistence operations for the Event aggregate.
type EventRepository interface {
	// Save persists an Event. If the event includes a CommandAudit,
	// both are saved as part of the same aggregate.
	Save(ctx context.Context, event *Event) error

	// GetBySessionID retrieves the session_started event for the given session.
	GetBySessionID(ctx context.Context, sessionID types.SessionID) (*Event, error)
}
