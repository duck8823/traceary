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
	sessionLimit      int
	eventsPerSession  int
	memoryLimit       int
	memoryAsOf        domtypes.Optional[time.Time]
	timelineLimit     int
	timelineGapSecond int
	hotspotLimit      int
	hotspotLookback   time.Duration
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

// TimelineLimit returns the maximum number of timeline blocks to
// include. Values <= 0 instruct the usecase to skip the timeline panel
// entirely.
func (c ReplayCriteria) TimelineLimit() int { return c.timelineLimit }

// TimelineGapSeconds returns the idle-gap threshold (in seconds) used
// to segment events into work blocks. 0 falls back to the usecase
// default (15 minutes).
func (c ReplayCriteria) TimelineGapSeconds() int { return c.timelineGapSecond }

// HotspotLimit returns the maximum number of failure hotspots to
// include. Values <= 0 instruct the usecase to skip the hotspot
// panel entirely.
func (c ReplayCriteria) HotspotLimit() int { return c.hotspotLimit }

// HotspotLookback returns the lookback window used to source failure
// events. Zero falls back to the usecase default (last 7 days).
func (c ReplayCriteria) HotspotLookback() time.Duration { return c.hotspotLookback }

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

// TimelineLimit sets the maximum number of timeline blocks. Values
// <= 0 skip the panel.
func (b *ReplayCriteriaBuilder) TimelineLimit(value int) *ReplayCriteriaBuilder {
	b.criteria.timelineLimit = value
	return b
}

// TimelineGapSeconds sets the idle-gap threshold. 0 falls back to the
// usecase default.
func (b *ReplayCriteriaBuilder) TimelineGapSeconds(value int) *ReplayCriteriaBuilder {
	b.criteria.timelineGapSecond = value
	return b
}

// HotspotLimit sets the maximum number of failure hotspots. Values
// <= 0 skip the panel.
func (b *ReplayCriteriaBuilder) HotspotLimit(value int) *ReplayCriteriaBuilder {
	b.criteria.hotspotLimit = value
	return b
}

// HotspotLookback sets the lookback window for the failure hotspot
// query. Zero falls back to the usecase default.
func (b *ReplayCriteriaBuilder) HotspotLookback(value time.Duration) *ReplayCriteriaBuilder {
	b.criteria.hotspotLookback = value
	return b
}

// Build returns the finalized ReplayCriteria value.
func (b *ReplayCriteriaBuilder) Build() ReplayCriteria {
	return b.criteria
}
