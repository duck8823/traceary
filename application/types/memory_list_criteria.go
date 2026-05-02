package types

import (
	"slices"
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

var defaultActiveMemoryStatuses = []domtypes.MemoryStatus{
	domtypes.MemoryStatusCandidate,
	domtypes.MemoryStatusAccepted,
}

// DefaultActiveMemoryStatuses returns the statuses included in read paths
// when callers do not request explicit memory statuses.
func DefaultActiveMemoryStatuses() []domtypes.MemoryStatus {
	return slices.Clone(defaultActiveMemoryStatuses)
}

// MemoryListCriteria holds filter parameters for memory listing.
// Zero-value fields are ignored unless documented otherwise.
type MemoryListCriteria struct {
	limit                  int
	offset                 int
	scopes                 []domtypes.MemoryScope
	statuses               []domtypes.MemoryStatus
	memoryTypes            []domtypes.MemoryType
	sources                []domtypes.MemorySource
	asOf                   domtypes.Optional[time.Time]
	includeExpired         bool
	rememberIntentPriority bool
}

// Limit returns the maximum number of results.
func (c MemoryListCriteria) Limit() int { return c.limit }

// Offset returns the result offset.
func (c MemoryListCriteria) Offset() int { return c.offset }

// Scopes returns the typed scope filters.
func (c MemoryListCriteria) Scopes() []domtypes.MemoryScope { return slices.Clone(c.scopes) }

// Statuses returns the lifecycle status filters.
func (c MemoryListCriteria) Statuses() []domtypes.MemoryStatus { return slices.Clone(c.statuses) }

// MemoryTypes returns the memory type filters.
func (c MemoryListCriteria) MemoryTypes() []domtypes.MemoryType { return slices.Clone(c.memoryTypes) }

// Sources returns the memory source filters.
func (c MemoryListCriteria) Sources() []domtypes.MemorySource { return slices.Clone(c.sources) }

// AsOf returns the point-in-time at which validity should be evaluated.
// When present, a memory matches only if validFrom <= AsOf and
// (validTo is null or validTo > AsOf). The zero value (None) means
// "use the current wall clock at query time".
func (c MemoryListCriteria) AsOf() domtypes.Optional[time.Time] { return c.asOf }

// IncludeExpiredByValidity returns true when the caller asked to bypass
// the validity-window filter and return memories whose validTo is in
// the past as well. Defaults to false so the common case
// ("give me memories that are still valid right now") is the default.
func (c MemoryListCriteria) IncludeExpiredByValidity() bool { return c.includeExpired }

// RememberIntentPriority reports whether prioritized inbox source rows should
// be returned ahead of other rows in the result set. The flag is honoured at
// the query layer so pagination (--limit/--offset) is consistent with the
// displayed order. Used by the inbox view; default is false.
func (c MemoryListCriteria) RememberIntentPriority() bool { return c.rememberIntentPriority }

// MemoryListCriteriaBuilder builds a MemoryListCriteria value.
type MemoryListCriteriaBuilder struct {
	criteria MemoryListCriteria
}

// NewMemoryListCriteriaBuilder starts building with the given limit.
func NewMemoryListCriteriaBuilder(limit int) *MemoryListCriteriaBuilder {
	return &MemoryListCriteriaBuilder{criteria: MemoryListCriteria{limit: limit}}
}

// Offset sets the number of results to skip.
func (b *MemoryListCriteriaBuilder) Offset(offset int) *MemoryListCriteriaBuilder {
	b.criteria.offset = offset
	return b
}

// Scope appends a typed scope filter.
func (b *MemoryListCriteriaBuilder) Scope(scope domtypes.MemoryScope) *MemoryListCriteriaBuilder {
	if scope != nil {
		b.criteria.scopes = append(b.criteria.scopes, scope)
	}
	return b
}

// Scopes replaces the typed scope filters.
func (b *MemoryListCriteriaBuilder) Scopes(scopes []domtypes.MemoryScope) *MemoryListCriteriaBuilder {
	b.criteria.scopes = slices.Clone(scopes)
	return b
}

// Status appends a lifecycle status filter.
func (b *MemoryListCriteriaBuilder) Status(status domtypes.MemoryStatus) *MemoryListCriteriaBuilder {
	if status != "" {
		b.criteria.statuses = append(b.criteria.statuses, status)
	}
	return b
}

// Statuses replaces the lifecycle status filters.
func (b *MemoryListCriteriaBuilder) Statuses(statuses []domtypes.MemoryStatus) *MemoryListCriteriaBuilder {
	b.criteria.statuses = slices.Clone(statuses)
	return b
}

// MemoryType appends a memory type filter.
func (b *MemoryListCriteriaBuilder) MemoryType(memoryType domtypes.MemoryType) *MemoryListCriteriaBuilder {
	if memoryType != "" {
		b.criteria.memoryTypes = append(b.criteria.memoryTypes, memoryType)
	}
	return b
}

// MemoryTypes replaces the memory type filters.
func (b *MemoryListCriteriaBuilder) MemoryTypes(memoryTypes []domtypes.MemoryType) *MemoryListCriteriaBuilder {
	b.criteria.memoryTypes = slices.Clone(memoryTypes)
	return b
}

// Source appends a memory source filter.
func (b *MemoryListCriteriaBuilder) Source(source domtypes.MemorySource) *MemoryListCriteriaBuilder {
	if source != "" {
		b.criteria.sources = append(b.criteria.sources, source)
	}
	return b
}

// Sources replaces the memory source filters.
func (b *MemoryListCriteriaBuilder) Sources(sources []domtypes.MemorySource) *MemoryListCriteriaBuilder {
	b.criteria.sources = slices.Clone(sources)
	return b
}

// AsOf sets the point-in-time at which validity windows are evaluated.
// Zero values are ignored.
func (b *MemoryListCriteriaBuilder) AsOf(asOf time.Time) *MemoryListCriteriaBuilder {
	if !asOf.IsZero() {
		b.criteria.asOf = domtypes.Some(asOf)
	}
	return b
}

// IncludeExpiredByValidity toggles whether memories whose validTo is in
// the past are included in results. Default is false.
func (b *MemoryListCriteriaBuilder) IncludeExpiredByValidity(include bool) *MemoryListCriteriaBuilder {
	b.criteria.includeExpired = include
	return b
}

// RememberIntentPriority toggles whether prioritized inbox source rows should
// be returned ahead of other rows in the result set. The flag is honoured at
// the query layer so pagination is consistent with the displayed order.
// Default is false.
func (b *MemoryListCriteriaBuilder) RememberIntentPriority(priority bool) *MemoryListCriteriaBuilder {
	b.criteria.rememberIntentPriority = priority
	return b
}

// Build finalizes and returns the MemoryListCriteria.
func (b *MemoryListCriteriaBuilder) Build() MemoryListCriteria {
	return b.criteria
}
