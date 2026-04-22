package types

import (
	"slices"
	"time"

	"github.com/duck8823/traceary/domain/model"
)

// ReplayBundle is the cross-aggregate result returned by ReplayUsecase.
// It carries the sessions (with their per-session events), the memory
// panel scoped to those sessions' workspaces, a timeline of recent
// work blocks (same shape as `traceary timeline`), and a failure
// hotspot ranking of command_executed events with non-zero exit code.
// Presentation callers render the replay HTML from this snapshot
// without re-querying the store.
type ReplayBundle struct {
	generatedAt     time.Time
	sessions        []ReplayBundleSession
	memories        []MemorySummary
	timelineBlocks  []TimelineBlock
	failureHotspots []ReplayFailureHotspot
}

// ReplayFailureHotspot clusters non-zero-exit-code command_executed
// events by normalized command prefix (first whitespace-delimited
// token) within a workspace, so operators can see where the failures
// pile up at a glance. Count is the number of failure events in the
// cluster and LastOccurredAt is the most recent of those events.
type ReplayFailureHotspot struct {
	command        string
	workspace      string
	count          int
	lastOccurredAt time.Time
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
func ReplayBundleOf(
	generatedAt time.Time,
	sessions []ReplayBundleSession,
	memories []MemorySummary,
	timelineBlocks []TimelineBlock,
	failureHotspots []ReplayFailureHotspot,
) ReplayBundle {
	copiedSessions := make([]ReplayBundleSession, len(sessions))
	for i, session := range sessions {
		copiedEvents := make([]*model.Event, len(session.events))
		copy(copiedEvents, session.events)
		copiedSessions[i] = ReplayBundleSession{summary: session.summary, events: copiedEvents}
	}
	return ReplayBundle{
		generatedAt:     generatedAt,
		sessions:        copiedSessions,
		memories:        slices.Clone(memories),
		timelineBlocks:  slices.Clone(timelineBlocks),
		failureHotspots: slices.Clone(failureHotspots),
	}
}

// ReplayFailureHotspotOf creates a ReplayFailureHotspot value.
func ReplayFailureHotspotOf(command, workspace string, count int, lastOccurredAt time.Time) ReplayFailureHotspot {
	return ReplayFailureHotspot{
		command:        command,
		workspace:      workspace,
		count:          count,
		lastOccurredAt: lastOccurredAt,
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

// TimelineBlocks returns the recent work blocks. Mutating the returned
// slice does not affect the bundle.
func (b ReplayBundle) TimelineBlocks() []TimelineBlock { return slices.Clone(b.timelineBlocks) }

// FailureHotspots returns the failure-hotspot ranking in descending
// count order. Mutating the returned slice does not affect the bundle.
func (b ReplayBundle) FailureHotspots() []ReplayFailureHotspot { return slices.Clone(b.failureHotspots) }

// Command returns the normalized command prefix the hotspot clusters
// around (for example "go" for any `go test` / `go vet` failure).
func (h ReplayFailureHotspot) Command() string { return h.command }

// Workspace returns the workspace the hotspot was observed in. Empty
// when the event did not carry a workspace.
func (h ReplayFailureHotspot) Workspace() string { return h.workspace }

// Count returns the number of failure events in the cluster.
func (h ReplayFailureHotspot) Count() int { return h.count }

// LastOccurredAt returns the most recent failure timestamp in the
// cluster.
func (h ReplayFailureHotspot) LastOccurredAt() time.Time { return h.lastOccurredAt }

// Summary returns the session summary inside a ReplayBundleSession.
func (s ReplayBundleSession) Summary() SessionSummary { return s.summary }

// Events returns a copy of the events loaded for this session.
func (s ReplayBundleSession) Events() []*model.Event { return slices.Clone(s.events) }
