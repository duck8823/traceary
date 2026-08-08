package model

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// SessionOrphanRangeRepository persists front-loaded orphan ranges (compact
// boundary) and answers the discovery questions gc needs. Recorded rows are a
// shortcut; ended/stale discovery from covers_to is the source of truth.
type SessionOrphanRangeRepository interface {
	// Record inserts an orphan range. Re-recording the same
	// (session_id, to_event_id) is a no-op.
	Record(ctx context.Context, orphan *SessionOrphanRange) error

	// DiscoverCandidates returns orphan ranges that still need a degraded
	// refinement:
	//   1. recorded rows whose session is still active (not ended, not stale)
	//      and whose to_event_id is not yet covered by the session refinement
	//   2. ended or stale sessions with material past covers_to (or the whole
	//      session when no refinement exists)
	// staleAfter is the same activity window as session gc (default 24h).
	DiscoverCandidates(ctx context.Context, staleAfter time.Duration, now time.Time) ([]*SessionOrphanRange, error)

	// LoadMaterial returns the mechanical-summary inputs for a range under
	// canonical event order (ts_norm(created_at), id).
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
