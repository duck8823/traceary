package model

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// ConsolidationRefineStamp is the amendment payload for an open request.
type ConsolidationRefineStamp struct {
	SessionID  types.SessionID
	Outcome    types.ConsolidationRefineOutcome
	Reason     string
	ProducedBy string
	Generation types.Optional[int]
	At         time.Time
}

// ConsolidationRequestRepository persists fold-request facts.
type ConsolidationRequestRepository interface {
	// Save inserts. Returns (false, nil) when (session_id, at_event_id) exists.
	Save(ctx context.Context, request *ConsolidationRequest) (bool, error)
	// FindLatestOpen returns the newest row for the session with no outcome.
	FindLatestOpen(ctx context.Context, sessionID types.SessionID) (types.Optional[*ConsolidationRequest], error)
	// FindLatest returns the newest row for the session regardless of outcome.
	FindLatest(ctx context.Context, sessionID types.SessionID) (types.Optional[*ConsolidationRequest], error)
	// MarkRefineOutcome stamps the newest open row. Returns (false, nil) if none.
	MarkRefineOutcome(ctx context.Context, stamp ConsolidationRefineStamp) (bool, error)
}
