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

// SearchSessionTierState is the session-tier disposition for one snapshot.
// ready: the published generation was consulted. not_ready: it was refused.
// not_applicable: the tier was never opened (empty query, --kind, or a
// page after the first).
type SearchSessionTierState string

const (
	// SearchSessionTierReady means the published generation was consulted.
	SearchSessionTierReady SearchSessionTierState = "ready"
	// SearchSessionTierNotReady means the snapshot refused the session tier.
	SearchSessionTierNotReady SearchSessionTierState = "not_ready"
	// SearchSessionTierNotApplicable means the tier was never opened.
	SearchSessionTierNotApplicable SearchSessionTierState = "not_applicable"
)

// SearchSessionPage is hits plus the readiness that explains an empty page,
// taken from the same read snapshot.
type SearchSessionPage struct {
	hits  []SearchSessionHit
	state SearchSessionTierState
}

// SearchSessionPageOf constructs a SearchSessionPage.
func SearchSessionPageOf(hits []SearchSessionHit, state SearchSessionTierState) SearchSessionPage {
	if hits == nil {
		hits = []SearchSessionHit{}
	}
	return SearchSessionPage{hits: hits, state: state}
}

// Hits returns the session-tier matches from this snapshot.
func (p SearchSessionPage) Hits() []SearchSessionHit { return p.hits }

// State reports whether the session tier was consulted, refused, or skipped.
func (p SearchSessionPage) State() SearchSessionTierState { return p.state }
