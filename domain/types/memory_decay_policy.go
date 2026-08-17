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
// Explicit user intent (manual / remember-intent), compact-summary, and
// imports never decay — they wait for a human look (#2062).
func DefaultDecaySources() []MemorySource {
	return []MemorySource{
		MemorySourceExtracted,
		MemorySourceExtractedHidden,
	}
}

// CandidateTTLApplies reports whether a candidate should carry a scheduled
// expires_at stamp (created_at + DefaultMemoryDecayOlderThan).
func CandidateTTLApplies(source MemorySource) bool {
	return source == MemorySourceExtracted || source == MemorySourceExtractedHidden
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

// DecayGrantStart is the start of the current TTL grant. Unstamped rows
// use created_at. Stamped rows use expires_at minus the default TTL so a
// restore restamp is a fresh window and --older-than still shortens it.
func DecayGrantStart(createdAt time.Time, expiresAt Optional[time.Time]) time.Time {
	if exp, ok := expiresAt.Value(); ok {
		return exp.UTC().Add(-DefaultMemoryDecayOlderThan)
	}
	return createdAt.UTC()
}

// CandidateDue reports whether the grant is older than the policy window.
func (p MemoryDecayPolicy) CandidateDue(createdAt time.Time, expiresAt Optional[time.Time], now time.Time) bool {
	return DecayGrantStart(createdAt, expiresAt).Before(now.UTC().Add(-p.olderThan))
}
