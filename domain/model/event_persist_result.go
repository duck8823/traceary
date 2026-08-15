package model

import "github.com/duck8823/traceary/domain/types"

// EventPersistResult is the write-side outcome of Save / SaveWithAudit /
// SaveBoundary. Inserted is false on an exact hook redelivery.
type EventPersistResult struct {
	inserted    bool
	persistedID types.EventID
}

// EventPersistResultOf reports whether a row was inserted and which event
// id is now the store's canonical identity for that delivery.
func EventPersistResultOf(inserted bool, persistedID types.EventID) EventPersistResult {
	return EventPersistResult{inserted: inserted, persistedID: persistedID}
}

// Inserted reports whether a new events row was written.
func (r EventPersistResult) Inserted() bool { return r.inserted }

// PersistedID is the store's event id: the constructed id on insert, or
// the existing row's id on exact redelivery.
func (r EventPersistResult) PersistedID() types.EventID { return r.persistedID }

// ApplyEventPersistResult stamps the persist outcome onto the in-memory
// event so callers of Save do not have to change their error-only
// signatures to learn insert vs redelivery.
func ApplyEventPersistResult(event *Event, result EventPersistResult) {
	if event == nil {
		return
	}
	event.RecordPersistOutcome(result.Inserted(), result.PersistedID())
}
