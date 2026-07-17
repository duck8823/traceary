package types

import (
	"slices"
	"time"

	"golang.org/x/xerrors"
)

// MemoryDecayPolicy is the value object that decides whether a candidate
// memory is eligible for automatic expiry (non-destructive status transition).
type MemoryDecayPolicy struct {
	olderThan time.Duration
	sources   []MemorySource
}

// DefaultDecaySources are the auto-extraction sources that may decay.
// Explicit user intent (manual / remember-intent) and imports never decay.
func DefaultDecaySources() []MemorySource {
	return []MemorySource{
		MemorySourceExtracted,
		MemorySourceExtractedHidden,
		MemorySourceCompactSummary,
	}
}

// DefaultMemoryDecayOlderThan is 30 days — more conservative than the legacy
// 14-day hard DELETE of stale extracted candidates.
const DefaultMemoryDecayOlderThan = 720 * time.Hour

// MemoryDecayPolicyOf builds a policy. olderThan must be positive; sources
// default to DefaultDecaySources when empty.
func MemoryDecayPolicyOf(olderThan time.Duration, sources []MemorySource) (MemoryDecayPolicy, error) {
	if olderThan <= 0 {
		return MemoryDecayPolicy{}, xerrors.Errorf("memory decay older-than must be positive")
	}
	if len(sources) == 0 {
		sources = DefaultDecaySources()
	}
	// Defensive copy.
	copied := append([]MemorySource(nil), sources...)
	return MemoryDecayPolicy{olderThan: olderThan, sources: copied}, nil
}

// OlderThan returns the age threshold.
func (p MemoryDecayPolicy) OlderThan() time.Duration { return p.olderThan }

// Sources returns the allowed auto sources.
func (p MemoryDecayPolicy) Sources() []MemorySource {
	return append([]MemorySource(nil), p.sources...)
}

// AllowsSource reports whether source is in the decay allow-list.
func (p MemoryDecayPolicy) AllowsSource(source MemorySource) bool {
	return slices.Contains(p.sources, source)
}
