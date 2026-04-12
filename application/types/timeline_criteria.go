package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// TimelineCriteria holds filter parameters for work timeline block listing.
// Zero-value fields are ignored (no filter applied).
type TimelineCriteria struct {
	workspace  domtypes.Workspace
	from       time.Time
	to         time.Time
	gapSeconds int
	limit      int
}

// Workspace returns the workspace filter.
func (c TimelineCriteria) Workspace() domtypes.Workspace { return c.workspace }

// From returns the lower bound of the time range (inclusive).
func (c TimelineCriteria) From() time.Time { return c.from }

// To returns the upper bound of the time range (exclusive).
func (c TimelineCriteria) To() time.Time { return c.to }

// GapSeconds returns the idle gap threshold in seconds.
func (c TimelineCriteria) GapSeconds() int { return c.gapSeconds }

// Limit returns the maximum number of blocks to return.
func (c TimelineCriteria) Limit() int { return c.limit }

// TimelineCriteriaBuilder builds a TimelineCriteria value.
type TimelineCriteriaBuilder struct {
	criteria TimelineCriteria
}

// NewTimelineCriteriaBuilder starts building with the given limit.
// Limit is required; other fields are optional.
func NewTimelineCriteriaBuilder(limit int) *TimelineCriteriaBuilder {
	return &TimelineCriteriaBuilder{criteria: TimelineCriteria{limit: limit}}
}

// Workspace sets the workspace filter.
func (b *TimelineCriteriaBuilder) Workspace(workspace domtypes.Workspace) *TimelineCriteriaBuilder {
	b.criteria.workspace = workspace
	return b
}

// From sets the lower bound of the time range (inclusive).
func (b *TimelineCriteriaBuilder) From(from time.Time) *TimelineCriteriaBuilder {
	b.criteria.from = from
	return b
}

// To sets the upper bound of the time range (exclusive).
func (b *TimelineCriteriaBuilder) To(to time.Time) *TimelineCriteriaBuilder {
	b.criteria.to = to
	return b
}

// GapSeconds sets the idle gap threshold in seconds.
func (b *TimelineCriteriaBuilder) GapSeconds(gapSeconds int) *TimelineCriteriaBuilder {
	b.criteria.gapSeconds = gapSeconds
	return b
}

// Build finalizes and returns the TimelineCriteria.
func (b *TimelineCriteriaBuilder) Build() TimelineCriteria {
	return b.criteria
}
