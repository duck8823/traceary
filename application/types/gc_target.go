package types

import "strings"

// GarbageCollectionTarget identifies which store records a gc run prunes.
type GarbageCollectionTarget string

const (
	// GarbageCollectionTargetEvents prunes event rows older than the cutoff.
	GarbageCollectionTargetEvents GarbageCollectionTarget = "events"
	// GarbageCollectionTargetSessions prunes ended sessions with no surviving events.
	GarbageCollectionTargetSessions GarbageCollectionTarget = "sessions"
	// GarbageCollectionTargetMemories prunes expired or superseded memories.
	GarbageCollectionTargetMemories GarbageCollectionTarget = "memories"
	// GarbageCollectionTargetMemoryEdges prunes closed or orphaned memory edges.
	GarbageCollectionTargetMemoryEdges GarbageCollectionTarget = "memory_edges"
	// GarbageCollectionTargetAll applies every garbage-collection target in dependency order.
	GarbageCollectionTargetAll GarbageCollectionTarget = "all"
)

// GarbageCollectionTargetFrom parses a CLI/API target value.
func GarbageCollectionTargetFrom(value string) (GarbageCollectionTarget, bool) {
	switch target := GarbageCollectionTarget(strings.TrimSpace(value)); target {
	case GarbageCollectionTargetEvents,
		// GarbageCollectionTargetSessions prunes ended sessions with no surviving events.
		GarbageCollectionTargetSessions,
		// GarbageCollectionTargetMemories prunes expired or superseded memories.
		GarbageCollectionTargetMemories,
		// GarbageCollectionTargetMemoryEdges prunes closed or orphaned memory edges.
		GarbageCollectionTargetMemoryEdges,
		// GarbageCollectionTargetAll applies every garbage-collection target in dependency order.
		GarbageCollectionTargetAll:
		return target, true
	default:
		return "", false
	}
}

// String returns the serialized target value.
func (t GarbageCollectionTarget) String() string { return string(t) }
