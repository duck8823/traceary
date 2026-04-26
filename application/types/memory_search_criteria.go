package types

import (
	"slices"
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemorySearchCriteria holds filter parameters for full-text memory search.
type MemorySearchCriteria struct {
	query          string
	limit          int
	offset         int
	scopes         []domtypes.MemoryScope
	statuses       []domtypes.MemoryStatus
	memoryTypes    []domtypes.MemoryType
	sources        []domtypes.MemorySource
	asOf           domtypes.Optional[time.Time]
	includeExpired bool
}

// Query returns the search query.
func (c MemorySearchCriteria) Query() string { return c.query }

// Limit returns the maximum number of results.
func (c MemorySearchCriteria) Limit() int { return c.limit }

// Offset returns the result offset.
func (c MemorySearchCriteria) Offset() int { return c.offset }

// Scopes returns the typed scope filters.
func (c MemorySearchCriteria) Scopes() []domtypes.MemoryScope { return slices.Clone(c.scopes) }

// Statuses returns the lifecycle status filters.
func (c MemorySearchCriteria) Statuses() []domtypes.MemoryStatus { return slices.Clone(c.statuses) }

// MemoryTypes returns the memory type filters.
func (c MemorySearchCriteria) MemoryTypes() []domtypes.MemoryType { return slices.Clone(c.memoryTypes) }

// Sources returns the memory source filters.
func (c MemorySearchCriteria) Sources() []domtypes.MemorySource { return slices.Clone(c.sources) }

// AsOf returns the point-in-time at which validity windows are
// evaluated. See MemoryListCriteria.AsOf for semantics.
func (c MemorySearchCriteria) AsOf() domtypes.Optional[time.Time] { return c.asOf }

// IncludeExpiredByValidity returns true when the caller asked to bypass
// the validity-window filter. See MemoryListCriteria.IncludeExpiredByValidity.
func (c MemorySearchCriteria) IncludeExpiredByValidity() bool { return c.includeExpired }

// MemorySearchCriteriaBuilder builds a MemorySearchCriteria value.
type MemorySearchCriteriaBuilder struct {
	criteria MemorySearchCriteria
}

// NewMemorySearchCriteriaBuilder starts building with the given limit.
func NewMemorySearchCriteriaBuilder(limit int) *MemorySearchCriteriaBuilder {
	return &MemorySearchCriteriaBuilder{criteria: MemorySearchCriteria{limit: limit}}
}

// Query sets the search query string.
func (b *MemorySearchCriteriaBuilder) Query(query string) *MemorySearchCriteriaBuilder {
	b.criteria.query = query
	return b
}

// Offset sets the result offset.
func (b *MemorySearchCriteriaBuilder) Offset(offset int) *MemorySearchCriteriaBuilder {
	b.criteria.offset = offset
	return b
}

// Scope appends a typed scope filter.
func (b *MemorySearchCriteriaBuilder) Scope(scope domtypes.MemoryScope) *MemorySearchCriteriaBuilder {
	if scope != nil {
		b.criteria.scopes = append(b.criteria.scopes, scope)
	}
	return b
}

// Scopes replaces the typed scope filters.
func (b *MemorySearchCriteriaBuilder) Scopes(scopes []domtypes.MemoryScope) *MemorySearchCriteriaBuilder {
	b.criteria.scopes = slices.Clone(scopes)
	return b
}

// Status appends a lifecycle status filter.
func (b *MemorySearchCriteriaBuilder) Status(status domtypes.MemoryStatus) *MemorySearchCriteriaBuilder {
	if status != "" {
		b.criteria.statuses = append(b.criteria.statuses, status)
	}
	return b
}

// Statuses replaces the lifecycle status filters.
func (b *MemorySearchCriteriaBuilder) Statuses(statuses []domtypes.MemoryStatus) *MemorySearchCriteriaBuilder {
	b.criteria.statuses = slices.Clone(statuses)
	return b
}

// MemoryType appends a memory type filter.
func (b *MemorySearchCriteriaBuilder) MemoryType(memoryType domtypes.MemoryType) *MemorySearchCriteriaBuilder {
	if memoryType != "" {
		b.criteria.memoryTypes = append(b.criteria.memoryTypes, memoryType)
	}
	return b
}

// MemoryTypes replaces the memory type filters.
func (b *MemorySearchCriteriaBuilder) MemoryTypes(memoryTypes []domtypes.MemoryType) *MemorySearchCriteriaBuilder {
	b.criteria.memoryTypes = slices.Clone(memoryTypes)
	return b
}

// Sources replaces the memory source filters.
func (b *MemorySearchCriteriaBuilder) Sources(sources []domtypes.MemorySource) *MemorySearchCriteriaBuilder {
	b.criteria.sources = slices.Clone(sources)
	return b
}

// AsOf sets the point-in-time at which validity windows are evaluated.
// Zero values are ignored.
func (b *MemorySearchCriteriaBuilder) AsOf(asOf time.Time) *MemorySearchCriteriaBuilder {
	if !asOf.IsZero() {
		b.criteria.asOf = domtypes.Some(asOf)
	}
	return b
}

// IncludeExpiredByValidity toggles whether memories whose validTo is in
// the past are included in results. Default is false.
func (b *MemorySearchCriteriaBuilder) IncludeExpiredByValidity(include bool) *MemorySearchCriteriaBuilder {
	b.criteria.includeExpired = include
	return b
}

// Build finalizes and returns the MemorySearchCriteria.
func (b *MemorySearchCriteriaBuilder) Build() MemorySearchCriteria {
	return b.criteria
}
