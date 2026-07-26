package types

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventPageAnchor identifies the last event returned by a descending event
// page. Persistence adapters compare the normalized created-at value and event
// ID together so events sharing one timestamp are neither skipped nor repeated.
type EventPageAnchor struct {
	createdAt time.Time
	eventID   domtypes.EventID
}

// EventPageAnchorOf creates a complete event page anchor.
func EventPageAnchorOf(createdAt time.Time, eventID domtypes.EventID) (EventPageAnchor, error) {
	if createdAt.IsZero() {
		return EventPageAnchor{}, xerrors.Errorf("event page anchor created-at must not be zero")
	}
	if strings.TrimSpace(eventID.String()) == "" {
		return EventPageAnchor{}, xerrors.Errorf("event page anchor event ID must not be empty")
	}
	return EventPageAnchor{createdAt: createdAt.UTC(), eventID: eventID}, nil
}

// IsZero reports whether this criteria has no continuation anchor.
func (a EventPageAnchor) IsZero() bool {
	return a.createdAt.IsZero() && strings.TrimSpace(a.eventID.String()) == ""
}

// CreatedAt returns the anchor timestamp.
func (a EventPageAnchor) CreatedAt() time.Time { return a.createdAt }

// EventID returns the anchor event ID.
func (a EventPageAnchor) EventID() domtypes.EventID { return a.eventID }
