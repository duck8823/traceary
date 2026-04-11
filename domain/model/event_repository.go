package model

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// ErrSessionStartedEventNotFound indicates no session_started event exists for the given session.
var ErrSessionStartedEventNotFound = xerrors.New("session_started event was not found for the target session")

// EventRepository defines persistence operations for the Event aggregate.
type EventRepository interface {
	// Save persists an Event. If the event includes a CommandAudit,
	// both are saved as part of the same aggregate.
	Save(ctx context.Context, event *Event) error

	// GetSessionStartedEvent retrieves the session_started event for the given session.
	// Returns ErrSessionStartedEventNotFound when no matching event exists.
	GetSessionStartedEvent(ctx context.Context, sessionID types.SessionID) (*Event, error)
}
