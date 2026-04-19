package types

import (
	"slices"

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
	limit       int
	offset      int
	scopes      []domtypes.MemoryScope
	statuses    []domtypes.MemoryStatus
	memoryTypes []domtypes.MemoryType
	sources     []domtypes.MemorySource
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

// Build finalizes and returns the MemoryListCriteria.
func (b *MemoryListCriteriaBuilder) Build() MemoryListCriteria {
	return b.criteria
}
