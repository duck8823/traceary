package types

import (
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventWriteResult is the application-layer outcome of Log / Audit.
// Event is the constructed write with the store's canonical id. Inserted
// is false when an exact hook redelivery reused an existing row (#1710).
type EventWriteResult struct {
	event    *model.Event
	inserted bool
}

// EventWriteResultOf pairs a persisted (or reused) event with whether
// this call inserted it.
func EventWriteResultOf(event *model.Event, inserted bool) EventWriteResult {
	return EventWriteResult{event: event, inserted: inserted}
}

// Event returns the write, or nil when the call failed before constructing one.
func (r EventWriteResult) Event() *model.Event { return r.event }

// Inserted reports whether a new events row was written.
func (r EventWriteResult) Inserted() bool { return r.inserted }

// EventID is the store's canonical id, or empty when Event is nil.
func (r EventWriteResult) EventID() domtypes.EventID {
	if r.event == nil {
		return ""
	}
	return r.event.EventID()
}
