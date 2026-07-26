package types

import "golang.org/x/xerrors"

const (
	// DefaultEventResponseItemLimit is the safe page size for MCP event tools.
	DefaultEventResponseItemLimit = 20
	// MaxEventResponseItemLimit bounds one MCP event response.
	MaxEventResponseItemLimit = 100
	// DefaultEventResponseBodyRuneLimit is the default visible-text budget per event.
	DefaultEventResponseBodyRuneLimit = 500
	// MaxEventResponseBodyRuneLimit bounds a bounded body projection.
	MaxEventResponseBodyRuneLimit = 16 * 1024
	// MaxEventResponseAggregateBodyBytes bounds encoded body and body_blocks data.
	MaxEventResponseAggregateBodyBytes = 64 * 1024
)

// EventResponseBudget is the transport-neutral limit policy for an event page.
// BodyRuneLimit is zero only for an explicit full-body projection; its encoded
// response remains governed by AggregateBodyBytes.
type EventResponseBudget struct {
	itemLimit          int
	bodyRuneLimit      int
	aggregateBodyBytes int
}

// NewEventResponseBudget validates a resolved MCP event response budget.
func NewEventResponseBudget(itemLimit, bodyRuneLimit int) (EventResponseBudget, error) {
	if itemLimit < 1 || itemLimit > MaxEventResponseItemLimit {
		return EventResponseBudget{}, xerrors.Errorf("event response item limit must be between 1 and %d", MaxEventResponseItemLimit)
	}
	if bodyRuneLimit < 0 || bodyRuneLimit > MaxEventResponseBodyRuneLimit {
		return EventResponseBudget{}, xerrors.Errorf("event response body limit must be between 0 and %d", MaxEventResponseBodyRuneLimit)
	}
	return EventResponseBudget{
		itemLimit:          itemLimit,
		bodyRuneLimit:      bodyRuneLimit,
		aggregateBodyBytes: MaxEventResponseAggregateBodyBytes,
	}, nil
}

// ItemLimit returns the maximum events in a response page.
func (b EventResponseBudget) ItemLimit() int { return b.itemLimit }

// BodyRuneLimit returns the per-event visible-text limit, or zero for full body.
func (b EventResponseBudget) BodyRuneLimit() int { return b.bodyRuneLimit }

// AggregateBodyBytes returns the maximum encoded body payload for the page.
func (b EventResponseBudget) AggregateBodyBytes() int { return b.aggregateBodyBytes }

// EventPageExtent describes the observed coverage of a bounded page without
// exposing storage implementation details.
type EventPageExtent struct {
	candidateCount int
	returnedCount  int
	hasMore        bool
	partial        bool
	reasons        []string
}

// NewEventPageExtent creates a page extent. Reasons are copied so callers
// cannot mutate the response after construction.
func NewEventPageExtent(candidateCount, returnedCount int, hasMore, partial bool, reasons []string) (EventPageExtent, error) {
	if candidateCount < 0 || returnedCount < 0 || returnedCount > candidateCount {
		return EventPageExtent{}, xerrors.Errorf("event page extent has invalid candidate/returned counts")
	}
	return EventPageExtent{
		candidateCount: candidateCount,
		returnedCount:  returnedCount,
		hasMore:        hasMore,
		partial:        partial,
		reasons:        append([]string(nil), reasons...),
	}, nil
}

// CandidateCount returns the body-free candidates examined for this page.
func (e EventPageExtent) CandidateCount() int { return e.candidateCount }

// ReturnedCount returns the records included in the response.
func (e EventPageExtent) ReturnedCount() int { return e.returnedCount }

// HasMore reports whether the body-free probe observed another candidate.
func (e EventPageExtent) HasMore() bool { return e.hasMore }

// Partial reports whether the response omitted a later page or body data.
func (e EventPageExtent) Partial() bool { return e.partial }

// Reasons returns a copy of the machine-readable partial reasons.
func (e EventPageExtent) Reasons() []string { return append([]string(nil), e.reasons...) }
