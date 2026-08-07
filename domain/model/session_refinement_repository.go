package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionRefinementRepository persists the one-row-per-session L2 summary.
type SessionRefinementRepository interface {
	// FindBySessionID returns the current refinement, or None when absent.
	FindBySessionID(ctx context.Context, sessionID types.SessionID) (types.Optional[*SessionRefinement], error)
	// SaveIfAdvances stores refinement only when the persisted row is still at
	// expectedGeneration (0 means "no row yet") and the incoming covers_to
	// strictly advances coverage under canonical event order. It returns false
	// when another writer won the race; the caller re-reads and decides again.
	SaveIfAdvances(ctx context.Context, refinement *SessionRefinement, expectedGeneration int) (bool, error)
}
