package model

import "context"

// EventRepository defines event persistence.
type EventRepository interface {
	// Save persists an event.
	Save(ctx context.Context, event *Event) error
}
