package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionEventOrderRepository answers the canonical-order questions the
// refinement write port needs about events. Canonical order is
// (ts_norm(created_at), id); created_at is variable-width RFC3339Nano and is
// not lexically ordered (#1185).
type SessionEventOrderRepository interface {
	EarliestEventID(ctx context.Context, sessionID types.SessionID) (types.Optional[types.EventID], error)
	FindEventSessionID(ctx context.Context, eventID types.EventID) (types.Optional[types.SessionID], error)
	EventIsStrictlyAfter(ctx context.Context, left, right types.EventID) (bool, error)
}
