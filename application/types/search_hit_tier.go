package types

import (
	"github.com/duck8823/traceary/domain/model"
)

// SearchHitTier names which two-tier lane produced a hit.
type SearchHitTier string

const (
	// SearchHitTierRefinement is a session row from session_refinements.
	SearchHitTierRefinement SearchHitTier = "refinement"
	// SearchHitTierFallback is an event or session row from the unindexed scan.
	SearchHitTierFallback SearchHitTier = "fallback"

	// SearchHitTierRankRefinement is the total-order rank of the refinement lane.
	SearchHitTierRankRefinement = 0
	// SearchHitTierRankFallback is the total-order rank of the fallback lane.
	SearchHitTierRankFallback = 1
)

// Rank returns the fixed total-order rank for this tier.
func (t SearchHitTier) Rank() int {
	switch t {
	case SearchHitTierRefinement:
		return SearchHitTierRankRefinement
	case SearchHitTierFallback:
		return SearchHitTierRankFallback
	default:
		return SearchHitTierRankFallback + 1
	}
}

// RefinementDisposition reports whether the refinement lane applied.
type RefinementDisposition string

const (
	// RefinementDispositionApplied means session_refinements were consulted.
	RefinementDispositionApplied RefinementDisposition = "applied"
	// RefinementDispositionKindExcluded means --kind made the lane not applicable.
	RefinementDispositionKindExcluded RefinementDisposition = "kind_excluded"
	// RefinementDispositionFailuresExcluded means --failures made the lane not applicable.
	RefinementDispositionFailuresExcluded RefinementDisposition = "failures_excluded"
)

// SearchEventHit is a fallback-lane event row plus the tier label.
type SearchEventHit struct {
	event *model.Event
	tier  SearchHitTier
}

// SearchEventHitOf constructs a labeled event hit. event must not be nil.
func SearchEventHitOf(event *model.Event, tier SearchHitTier) SearchEventHit {
	return SearchEventHit{event: event, tier: tier}
}

// Event returns the hydrated event. It may be nil only for a zero value.
func (h SearchEventHit) Event() *model.Event { return h.event }

// Tier returns the lane that produced this event hit.
func (h SearchEventHit) Tier() SearchHitTier { return h.tier }

// TwoTierSearchPage is the {events, sessions} envelope plus refinement disposition.
type TwoTierSearchPage struct {
	events                []SearchEventHit
	sessions              []SearchSessionHit
	refinementDisposition RefinementDisposition
	refinementMatchCount  int
}

// TwoTierSearchPageOf constructs a page. Nil slices become empty slices.
func TwoTierSearchPageOf(
	events []SearchEventHit,
	sessions []SearchSessionHit,
	disposition RefinementDisposition,
	refinementMatchCount int,
) TwoTierSearchPage {
	if events == nil {
		events = []SearchEventHit{}
	}
	if sessions == nil {
		sessions = []SearchSessionHit{}
	}
	return TwoTierSearchPage{
		events:                events,
		sessions:              sessions,
		refinementDisposition: disposition,
		refinementMatchCount:  refinementMatchCount,
	}
}

// Events returns labeled event hits in total-order relative sequence.
func (p TwoTierSearchPage) Events() []SearchEventHit { return p.events }

// Sessions returns labeled session hits in total-order relative sequence.
func (p TwoTierSearchPage) Sessions() []SearchSessionHit { return p.sessions }

// RefinementDisposition reports whether the refinement lane applied.
func (p TwoTierSearchPage) RefinementDisposition() RefinementDisposition {
	return p.refinementDisposition
}

// RefinementMatchCount is how many refinement rows matched before exclusion
// and paging. CLI uses it to decide whether a kind/failures notice is warranted.
func (p TwoTierSearchPage) RefinementMatchCount() int { return p.refinementMatchCount }
