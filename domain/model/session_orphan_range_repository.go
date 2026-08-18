package model

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// SessionOrphanCandidates is one bounded page of discovery results.
type SessionOrphanCandidates struct {
	// Ranges holds at most the requested limit of candidates.
	Ranges []*SessionOrphanRange
	// HasMore reports that at least one further candidate exists beyond Ranges.
	// It is computed from valid candidates, never from raw row counts, so a
	// caller can trust "false" to mean "the backlog is drained".
	HasMore bool
}

// SessionOrphanRangeRepository persists front-loaded orphan ranges (compact
// boundary) and answers the discovery questions gc needs. Recorded rows are a
// shortcut; ended/stale discovery from covers_to is the source of truth.
type SessionOrphanRangeRepository interface {
	// Record inserts an orphan range. Re-recording the same
	// (session_id, to_event_id) is a no-op.
	Record(ctx context.Context, orphan *SessionOrphanRange) error

	// DiscoverCandidates returns at most limit orphan ranges that still need a
	// degraded refinement, and whether more remain beyond them:
	//   1. recorded rows whose session is still active (not ended, not stale)
	//      and whose to_event_id is not yet covered by the session refinement
	//   2. ended or stale sessions with material past covers_to (or the whole
	//      session when no refinement exists)
	//   3. sessions that are neither ended nor stale (an always-on session
	//      never satisfies source 2's predicate) but hold material past
	//      covers_to that is older than retentionCutoff. The folded terminus
	//      is the last-observed event strictly before retentionCutoff, never
	//      "now" or the session's true latest event: material at or after
	//      retentionCutoff is left uncovered so a later activity burst starts
	//      a fresh orphan range instead of being claimed as already folded
	//      (#1724). A recorded marker (source 1) for the same session is
	//      skipped in favour of this source when this source also matches,
	//      the same way source 1 already defers to source 2.
	// staleAfter is the same activity window as doctor/hook stale GC (default 24h); it
	// bounds sources 1 and 2. retentionCutoff bounds source 3; the zero value
	// disables source 3 entirely (callers that do not know the compact
	// retention cutoff keep today's two-source behaviour unchanged).
	// limit bounds the work one pass performs; it is a query bound, not a
	// persistence detail. limit must be greater than zero.
	// This asks a question; it changes nothing.
	DiscoverCandidates(
		ctx context.Context, staleAfter time.Duration, now time.Time, retentionCutoff time.Time, limit int,
	) (SessionOrphanCandidates, error)

	// LoadMaterial returns the mechanical-summary inputs for a range under
	// canonical event order (ts_norm(created_at), id). Like DiscoverCandidates,
	// it only reads.
	LoadMaterial(ctx context.Context, sessionID types.SessionID, fromExclusive types.Optional[types.EventID], toInclusive types.EventID) (SessionOrphanMaterial, error)
}

// SessionOrphanMaterial is the LLM-free data used to build a degraded summary.
type SessionOrphanMaterial struct {
	// FirstCreatedAt is the earliest event timestamp in the range.
	FirstCreatedAt time.Time
	// LastCreatedAt is the latest event timestamp in the range.
	LastCreatedAt time.Time
	// KindCounts maps event kind → count within the range.
	KindCounts map[string]int
	// Commands lists command_text values in canonical event order (may repeat).
	Commands []string
	// EventCount is the total number of events in the range.
	EventCount int
}
