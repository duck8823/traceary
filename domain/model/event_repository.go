package model

import "context"

// EventRepository persists Event aggregates.
type EventRepository interface {
	// Save persists a single event.
	Save(ctx context.Context, event *Event) error
	// SaveWithAudit persists an event together with its command audit.
	SaveWithAudit(ctx context.Context, event *Event, audit *CommandAudit) error
}
