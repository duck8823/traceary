package model

import "context"

// EventRepository defines persistence operations for the Event aggregate.
type EventRepository interface {
	// Save persists an Event. If the event includes a CommandAudit,
	// both are saved as part of the same aggregate.
	Save(ctx context.Context, event *Event) error
}
