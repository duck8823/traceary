package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// SessionOrphanRange is a range of a session's events past refinement coverage
// that can no longer be folded by an agent. Discovery does not depend on a
// recorded row: covers_to on an ended/stale session is the source of truth.
// A compact marker only front-loads the same fact.
//
// FromEventID is an exclusive lower bound. None means the range starts at the
// session's first event. ToEventID is inclusive.
type SessionOrphanRange struct {
	sessionID         types.SessionID
	fromEventID       types.Optional[types.EventID]
	toEventID         types.EventID
	observedAt        time.Time
	earliestEventTime time.Time
}

// NewSessionOrphanRange constructs a validated orphan range. earliestEventTime
// is the created_at of the range's oldest event; it drives oldest-first
// discovery ordering and the CollectGarbage safety gate (#1721), and is
// distinct from observedAt (when the range was recorded or discovered).
func NewSessionOrphanRange(
	sessionID types.SessionID,
	fromEventID types.Optional[types.EventID],
	toEventID types.EventID,
	observedAt time.Time,
	earliestEventTime time.Time,
) (*SessionOrphanRange, error) {
	orphan := &SessionOrphanRange{
		sessionID:         sessionID,
		fromEventID:       fromEventID,
		toEventID:         toEventID,
		observedAt:        observedAt.UTC(),
		earliestEventTime: earliestEventTime.UTC(),
	}
	if err := orphan.validate(); err != nil {
		return nil, err
	}
	return orphan, nil
}

// SessionOrphanRangeOf restores an orphan range from persisted values.
func SessionOrphanRangeOf(
	sessionID types.SessionID,
	fromEventID types.Optional[types.EventID],
	toEventID types.EventID,
	observedAt time.Time,
	earliestEventTime time.Time,
) (*SessionOrphanRange, error) {
	return NewSessionOrphanRange(sessionID, fromEventID, toEventID, observedAt, earliestEventTime)
}

func (r *SessionOrphanRange) validate() error {
	if r == nil {
		return xerrors.Errorf("session orphan range must not be nil: %w", ErrInvalidSessionOrphanRange)
	}
	if strings.TrimSpace(r.sessionID.String()) == "" {
		return xerrors.Errorf("session id is required: %w", ErrInvalidSessionOrphanRange)
	}
	if strings.TrimSpace(r.toEventID.String()) == "" {
		return xerrors.Errorf("to_event_id is required: %w", ErrInvalidSessionOrphanRange)
	}
	if from, ok := r.fromEventID.Value(); ok && strings.TrimSpace(from.String()) == "" {
		return xerrors.Errorf("from_event_id must not be empty when present: %w", ErrInvalidSessionOrphanRange)
	}
	if r.observedAt.IsZero() {
		return xerrors.Errorf("observed_at is required: %w", ErrInvalidSessionOrphanRange)
	}
	if r.earliestEventTime.IsZero() {
		return xerrors.Errorf("earliest_event_time is required: %w", ErrInvalidSessionOrphanRange)
	}
	return nil
}

// SessionID returns the owning session.
func (r *SessionOrphanRange) SessionID() types.SessionID { return r.sessionID }

// FromEventID returns the exclusive lower bound, or None when the range starts
// at the session's first event.
func (r *SessionOrphanRange) FromEventID() types.Optional[types.EventID] { return r.fromEventID }

// ToEventID returns the inclusive upper bound event id.
func (r *SessionOrphanRange) ToEventID() types.EventID { return r.toEventID }

// ObservedAt returns when the orphan range was recorded or discovered.
func (r *SessionOrphanRange) ObservedAt() time.Time { return r.observedAt }

// EarliestEventTime returns the created_at of the range's oldest event. It is
// the ordering key for oldest-first discovery and the CollectGarbage safety
// gate (#1721).
func (r *SessionOrphanRange) EarliestEventTime() time.Time { return r.earliestEventTime }

// ErrInvalidSessionOrphanRange indicates an orphan-range invariant was violated.
var ErrInvalidSessionOrphanRange = xerrors.New("invalid session orphan range")
