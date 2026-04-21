package types

import (
	"slices"
	"time"

	"github.com/duck8823/traceary/domain/model"
)

// ReplayBundle is the cross-aggregate result returned by ReplayUsecase.
// It carries the sessions (with their per-session events) plus the
// memory panel scoped to those sessions' workspaces, so presentation
// callers can render the replay HTML without re-querying the store.
type ReplayBundle struct {
	generatedAt time.Time
	sessions    []ReplayBundleSession
	memories    []MemorySummary
}

// ReplayBundleSession pairs a session summary with the events loaded
// for it inside the replay bundle. The event list is already capped at
// ReplayCriteria.EventsPerSession.
type ReplayBundleSession struct {
	summary SessionSummary
	events  []*model.Event
}

// ReplayBundleOf creates a ReplayBundle and defensively copies its
// slices so the presentation caller cannot mutate the usecase's
// working set after it returns.
func ReplayBundleOf(generatedAt time.Time, sessions []ReplayBundleSession, memories []MemorySummary) ReplayBundle {
	copiedSessions := make([]ReplayBundleSession, len(sessions))
	for i, session := range sessions {
		copiedEvents := make([]*model.Event, len(session.events))
		copy(copiedEvents, session.events)
		copiedSessions[i] = ReplayBundleSession{summary: session.summary, events: copiedEvents}
	}
	return ReplayBundle{
		generatedAt: generatedAt,
		sessions:    copiedSessions,
		memories:    slices.Clone(memories),
	}
}

// ReplayBundleSessionOf creates a ReplayBundleSession pairing a summary
// with its event list.
func ReplayBundleSessionOf(summary SessionSummary, events []*model.Event) ReplayBundleSession {
	copiedEvents := make([]*model.Event, len(events))
	copy(copiedEvents, events)
	return ReplayBundleSession{summary: summary, events: copiedEvents}
}

// GeneratedAt returns the wall-clock time the bundle was assembled.
func (b ReplayBundle) GeneratedAt() time.Time { return b.generatedAt }

// Sessions returns the per-session slice. Mutating the returned slice
// does not affect the bundle.
func (b ReplayBundle) Sessions() []ReplayBundleSession {
	result := make([]ReplayBundleSession, len(b.sessions))
	for i, session := range b.sessions {
		result[i] = ReplayBundleSession{summary: session.summary, events: slices.Clone(session.events)}
	}
	return result
}

// Memories returns the memory panel in bundle order. The slice is a
// copy so the caller can mutate it safely.
func (b ReplayBundle) Memories() []MemorySummary { return slices.Clone(b.memories) }

// Summary returns the session summary inside a ReplayBundleSession.
func (s ReplayBundleSession) Summary() SessionSummary { return s.summary }

// Events returns a copy of the events loaded for this session.
func (s ReplayBundleSession) Events() []*model.Event { return slices.Clone(s.events) }
