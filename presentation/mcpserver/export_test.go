package mcpserver

import (
	"time"

	"github.com/duck8823/traceary/domain/model"
)

// ParseFlexibleTime exposes parseFlexibleTime for testing.
var ParseFlexibleTime = func(value string, endExclusive bool) (time.Time, error) {
	return parseFlexibleTime(value, endExclusive)
}

// ResolveLimit exposes resolveLimit for testing.
var ResolveLimit = resolveLimit

// ResolveOffset exposes resolveOffset for testing.
var ResolveOffset = resolveOffset

// ResolveBodyLimit exposes resolveBodyLimit for testing.
var ResolveBodyLimit = resolveBodyLimit

// ConvertEventsWithBodyLimit exposes convertEventsWithBodyLimit for testing.
var ConvertEventsWithBodyLimit = func(events []*model.Event, bodyLimit int) []EventOutput {
	out := convertEventsWithBodyLimit(events, bodyLimit)
	exposed := make([]EventOutput, len(out))
	for i, e := range out {
		body := ""
		if e.Body != nil {
			body = *e.Body
		}
		exposed[i] = EventOutput{
			EventID:       e.EventID,
			Body:          body,
			BodyTruncated: e.BodyTruncated,
			BodyLength:    e.BodyLength,
		}
	}
	return exposed
}

// EventOutput exposes the bits of eventOutput needed by tests.
type EventOutput struct {
	EventID       string
	Body          string
	BodyTruncated bool
	BodyLength    int
}

// DefaultListEventBodyLimit exposes defaultListEventBodyLimit for tests.
const DefaultListEventBodyLimit = defaultListEventBodyLimit
