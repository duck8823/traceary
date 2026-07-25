package types

import (
	"time"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventTimestampKind is the smallest body-free projection needed by compact
// timestamp/kind list output.
type EventTimestampKind struct {
	createdAt time.Time
	kind      domtypes.EventKind
}

// EventTimestampKindOf creates a compact timestamp/kind projection.
func EventTimestampKindOf(createdAt time.Time, kind domtypes.EventKind) (EventTimestampKind, error) {
	if createdAt.IsZero() {
		return EventTimestampKind{}, xerrors.Errorf("created at must not be zero")
	}
	return EventTimestampKind{createdAt: createdAt, kind: kind}, nil
}

// CreatedAt returns the event timestamp.
func (e EventTimestampKind) CreatedAt() time.Time { return e.createdAt }

// Kind returns the event kind.
func (e EventTimestampKind) Kind() domtypes.EventKind { return e.kind }
