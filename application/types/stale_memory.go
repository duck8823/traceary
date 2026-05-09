package types

import (
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// StaleMemoryReason explains why a durable-memory row should be surfaced in
// the operator-facing stale-memory pane.
type StaleMemoryReason string

const (
	// StaleMemoryReasonExpired covers memories whose lifecycle status is
	// expired or whose content-validity window has already closed.
	StaleMemoryReasonExpired StaleMemoryReason = "expired"
	// StaleMemoryReasonSuperseded covers memories that have been replaced but
	// still exist in the local store for audit / pruning.
	StaleMemoryReasonSuperseded StaleMemoryReason = "superseded"
	// StaleMemoryReasonOverlap covers memories that are part of a deterministic
	// dedupe overlap hygiene hit.
	StaleMemoryReasonOverlap StaleMemoryReason = "overlap"
)

// StaleMemoryReasonFrom validates a serialized stale-memory reason.
func StaleMemoryReasonFrom(value string) (StaleMemoryReason, error) {
	trimmed := strings.TrimSpace(value)
	switch StaleMemoryReason(trimmed) {
	case StaleMemoryReasonExpired, StaleMemoryReasonSuperseded, StaleMemoryReasonOverlap:
		return StaleMemoryReason(trimmed), nil
	default:
		return "", xerrors.Errorf("unknown stale memory reason: %s", value)
	}
}

// StaleMemoryRow is the read-side row rendered by the stale-memory top pane.
type StaleMemoryRow struct {
	summary MemorySummary
	reason  StaleMemoryReason
}

// StaleMemoryRowOf creates a stale-memory row from an existing memory summary.
func StaleMemoryRowOf(summary MemorySummary, reason StaleMemoryReason) (StaleMemoryRow, error) {
	if _, err := StaleMemoryReasonFrom(reason.String()); err != nil {
		return StaleMemoryRow{}, err
	}
	return StaleMemoryRow{summary: summary, reason: reason}, nil
}

// Summary returns the durable-memory summary for this stale row.
func (r StaleMemoryRow) Summary() MemorySummary { return r.summary }

// Reason returns the staleness reason.
func (r StaleMemoryRow) Reason() StaleMemoryReason { return r.reason }

// String returns the serialized reason value.
func (r StaleMemoryReason) String() string { return string(r) }

// StaleMemoryListCriteria holds the filters for read-side stale-memory lists.
type StaleMemoryListCriteria struct {
	limit  int
	offset int
	scopes []domtypes.MemoryScope
	asOf   domtypes.Optional[time.Time]
}

// Limit returns the maximum number of rows to return.
func (c StaleMemoryListCriteria) Limit() int { return c.limit }

// Offset returns the number of rows to skip after ordering.
func (c StaleMemoryListCriteria) Offset() int { return c.offset }

// Scopes returns the typed scope filters.
func (c StaleMemoryListCriteria) Scopes() []domtypes.MemoryScope { return slices.Clone(c.scopes) }

// AsOf returns the evaluation time for validity-window checks.
func (c StaleMemoryListCriteria) AsOf() domtypes.Optional[time.Time] { return c.asOf }

// StaleMemoryListCriteriaBuilder builds stale-memory list criteria.
type StaleMemoryListCriteriaBuilder struct {
	criteria StaleMemoryListCriteria
}

// NewStaleMemoryListCriteriaBuilder starts building with the given limit.
func NewStaleMemoryListCriteriaBuilder(limit int) *StaleMemoryListCriteriaBuilder {
	return &StaleMemoryListCriteriaBuilder{criteria: StaleMemoryListCriteria{limit: limit}}
}

// Offset sets the number of rows to skip.
func (b *StaleMemoryListCriteriaBuilder) Offset(offset int) *StaleMemoryListCriteriaBuilder {
	b.criteria.offset = offset
	return b
}

// Scope appends a typed scope filter.
func (b *StaleMemoryListCriteriaBuilder) Scope(scope domtypes.MemoryScope) *StaleMemoryListCriteriaBuilder {
	if scope != nil {
		b.criteria.scopes = append(b.criteria.scopes, scope)
	}
	return b
}

// Scopes replaces the typed scope filters.
func (b *StaleMemoryListCriteriaBuilder) Scopes(scopes []domtypes.MemoryScope) *StaleMemoryListCriteriaBuilder {
	b.criteria.scopes = slices.Clone(scopes)
	return b
}

// AsOf sets the evaluation time for validity-window checks.
func (b *StaleMemoryListCriteriaBuilder) AsOf(asOf time.Time) *StaleMemoryListCriteriaBuilder {
	if !asOf.IsZero() {
		b.criteria.asOf = domtypes.Some(asOf)
	}
	return b
}

// Build finalizes the criteria.
func (b *StaleMemoryListCriteriaBuilder) Build() StaleMemoryListCriteria {
	return b.criteria
}

// StaleMemoryListResult carries a paged stale-memory result plus the total
// stale count before LIMIT/OFFSET are applied.
type StaleMemoryListResult struct {
	count int
	items []StaleMemoryRow
}

// StaleMemoryListResultOf creates a stale-memory list result.
func StaleMemoryListResultOf(count int, items []StaleMemoryRow) (StaleMemoryListResult, error) {
	if count < 0 {
		return StaleMemoryListResult{}, xerrors.Errorf("stale memory count must not be negative")
	}
	if count < len(items) {
		return StaleMemoryListResult{}, xerrors.Errorf("stale memory count must be greater than or equal to item count")
	}
	return StaleMemoryListResult{count: count, items: slices.Clone(items)}, nil
}

// Count returns the total stale-memory count before paging.
func (r StaleMemoryListResult) Count() int { return r.count }

// Items returns the paged stale-memory rows.
func (r StaleMemoryListResult) Items() []StaleMemoryRow { return slices.Clone(r.items) }
