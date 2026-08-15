package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// EventRepository persists Event aggregates.
type EventRepository interface {
	// Save persists a single event.
	Save(ctx context.Context, event *Event) error
	// SaveWithAudit persists an event together with its command audit.
	SaveWithAudit(ctx context.Context, event *Event, audit *CommandAudit) error
	// DeleteTranscript removes one transcript event by id. It is a no-op
	// for a missing id or a non-transcript row (returns nil, 0 deleted).
	DeleteTranscript(ctx context.Context, eventID types.EventID) error
}
