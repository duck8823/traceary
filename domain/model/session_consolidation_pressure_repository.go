package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionConsolidationPressureRepository measures unrefined body-byte pressure
// for a session from the event store alone (no LLM, no writes).
//
// Canonical event order is (ts_norm(created_at), id). Plain TEXT comparison of
// variable-width RFC3339Nano is wrong (#1185); every "after covers_to" filter
// must go through ts_norm with id as the tie-break.
type SessionConsolidationPressureRepository interface {
	// SumBodyBytesAfter returns SUM(length(CAST(body AS BLOB))) over the
	// session's events that are strictly after coversTo. When coversTo is
	// None the whole session is summed (no refinement yet).
	SumBodyBytesAfter(
		ctx context.Context,
		sessionID types.SessionID,
		coversTo types.Optional[types.EventID],
	) (int64, error)
	// CountKindAfter returns COUNT(*) of events of kind strictly after `after`
	// under (created_at_norm, id) order. When after is None the whole session
	// is counted.
	CountKindAfter(
		ctx context.Context,
		sessionID types.SessionID,
		kind types.EventKind,
		after types.Optional[types.EventID],
	) (int64, error)
}
