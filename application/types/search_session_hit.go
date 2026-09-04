package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// SearchSessionHit is a session match from two-tier search.
// It points at a session trail rather than a single matching event line.
type SearchSessionHit struct {
	sessionID  domtypes.SessionID
	summary    string
	eventCount int
	startedAt  time.Time
	tier       SearchHitTier
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

// Summary returns the session summary excerpt.
func (h SearchSessionHit) Summary() string { return h.summary }

// EventCount returns how many events the session summary covers.
func (h SearchSessionHit) EventCount() int { return h.eventCount }

// StartedAt returns the session start time used for display.
func (h SearchSessionHit) StartedAt() time.Time { return h.startedAt }

// Tier returns the two-tier lane that produced this session hit.
func (h SearchSessionHit) Tier() SearchHitTier { return h.tier }

// WithTier returns a copy labeled with the given two-tier lane.
func (h SearchSessionHit) WithTier(tier SearchHitTier) SearchSessionHit {
	h.tier = tier
	return h
}
