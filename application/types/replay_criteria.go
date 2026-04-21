package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// ReplayCriteria holds the inputs the replay usecase consumes when it
// assembles a cross-aggregate bundle for a browser-viewable HTML
// export. Zero values fall back to sensible defaults at the usecase
// layer so presentation callers do not need to know the defaults.
type ReplayCriteria struct {
	sessionLimit     int
	eventsPerSession int
	memoryLimit      int
	memoryAsOf       domtypes.Optional[time.Time]
}

// SessionLimit returns the maximum number of recent sessions to load.
func (c ReplayCriteria) SessionLimit() int { return c.sessionLimit }

// EventsPerSession returns the cap applied to each session's event list.
func (c ReplayCriteria) EventsPerSession() int { return c.eventsPerSession }

// MemoryLimit returns the cap applied to the durable-memory panel.
// Any value <= 0 asks the usecase to skip the memory panel entirely;
// a positive value caps the row count returned.
func (c ReplayCriteria) MemoryLimit() int { return c.memoryLimit }

// MemoryAsOf returns the point-in-time used to evaluate memory validity
// windows. None means "use the current wall clock at query time".
func (c ReplayCriteria) MemoryAsOf() domtypes.Optional[time.Time] { return c.memoryAsOf }

// ReplayCriteriaBuilder constructs a ReplayCriteria.
type ReplayCriteriaBuilder struct {
	criteria ReplayCriteria
}

// NewReplayCriteriaBuilder starts building with the three session-side
// caps required by the replay export.
func NewReplayCriteriaBuilder(sessionLimit, eventsPerSession, memoryLimit int) *ReplayCriteriaBuilder {
	return &ReplayCriteriaBuilder{criteria: ReplayCriteria{
		sessionLimit:     sessionLimit,
		eventsPerSession: eventsPerSession,
		memoryLimit:      memoryLimit,
	}}
}

// MemoryAsOf sets the timestamp used to evaluate memory validity
// windows. Zero values are ignored so the replay defaults to "as of
// now".
func (b *ReplayCriteriaBuilder) MemoryAsOf(value time.Time) *ReplayCriteriaBuilder {
	if !value.IsZero() {
		b.criteria.memoryAsOf = domtypes.Some(value)
	}
	return b
}

// Build returns the finalized ReplayCriteria value.
func (b *ReplayCriteriaBuilder) Build() ReplayCriteria {
	return b.criteria
}
