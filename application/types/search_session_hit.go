package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// SearchSessionHit is a session-tier match for bounded projection search.
// It points at a session trail rather than a single matching event line.
type SearchSessionHit struct {
	sessionID  domtypes.SessionID
	summary    string
	eventCount int
	startedAt  time.Time
}

// SearchSessionHitOf constructs a SearchSessionHit.
func SearchSessionHitOf(
	sessionID domtypes.SessionID,
	summary string,
	eventCount int,
	startedAt time.Time,
) SearchSessionHit {
	return SearchSessionHit{
		sessionID:  sessionID,
		summary:    summary,
		eventCount: eventCount,
		startedAt:  startedAt,
	}
}

// SessionID returns the matched session identifier.
func (h SearchSessionHit) SessionID() domtypes.SessionID { return h.sessionID }

// Summary returns the projection summary excerpt for the session.
func (h SearchSessionHit) Summary() string { return h.summary }

// EventCount returns how many events the projection summary covers.
func (h SearchSessionHit) EventCount() int { return h.eventCount }

// StartedAt returns the session start time used for ordering and display.
func (h SearchSessionHit) StartedAt() time.Time { return h.startedAt }
