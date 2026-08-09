package queryservice

import (
	"fmt"

	"golang.org/x/xerrors"
)

// Event search can reject a request instead of returning an incomplete result
// or evaluating an unbounded legacy body scan.
var (
	ErrEventSearchScopeTooBroad   = xerrors.New("event search scope too broad")
	ErrEventSearchIndexIncomplete = xerrors.New("event search index incomplete")
)

// EventSearchUnavailableReason is the stable classification exposed to CLI and
// MCP consumers through EventSearchUnavailableError.
type EventSearchUnavailableReason string

const (
	// EventSearchUnavailableScopeTooBroad rejects a legacy fallback whose
	// structural/time candidate set cannot be proven below the hard cap.
	EventSearchUnavailableScopeTooBroad EventSearchUnavailableReason = "scope_too_broad"
	// EventSearchUnavailableIndexIncomplete rejects a query the index could not
	// finish answering: a legacy backfill still in progress, or a tiered walk
	// that ran out of candidate budget because no trustworthy fingerprint
	// generation was available to pre-filter with.
	EventSearchUnavailableIndexIncomplete EventSearchUnavailableReason = "index_incomplete"
)

// EventSearchUnavailableError describes why indexed search cannot safely
// satisfy a request. CandidateLimit is the maximum body-bearing legacy scope;
// CandidateCount is limit+1 when the cap was exceeded.
type EventSearchUnavailableError struct {
	Reason         EventSearchUnavailableReason
	CandidateLimit int
	CandidateCount int
}

func (e *EventSearchUnavailableError) Error() string {
	switch e.Reason {
	case EventSearchUnavailableIndexIncomplete:
		// Two very different situations reach this reason, and the message
		// must not name only one of them. A legacy store is mid-backfill; a
		// tiered store has no usable fingerprint generation, so every
		// candidate is decoded and the budget runs out on a wide query. Both
		// are fixed either by narrowing the query or by finishing the index,
		// so the message names both remedies rather than guessing which.
		return fmt.Sprintf(
			"event search could not finish within the candidate budget of %d rows; "+
				"add workspace/session and from/to bounds, "+
				"or complete the search projection with 'traceary store search-projection start'",
			e.CandidateLimit,
		)
	default:
		if e.CandidateCount > e.CandidateLimit {
			return fmt.Sprintf(
				"event search scope has more than %d legacy candidates; add narrower from/to bounds",
				e.CandidateLimit,
			)
		}
		return fmt.Sprintf(
			"event search scope is unbounded; add workspace/session and from/to bounds (legacy candidate limit %d)",
			e.CandidateLimit,
		)
	}
}

func (e *EventSearchUnavailableError) Unwrap() error {
	if e.Reason == EventSearchUnavailableIndexIncomplete {
		return ErrEventSearchIndexIncomplete
	}
	return ErrEventSearchScopeTooBroad
}
