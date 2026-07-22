package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// UsageObservationRepository atomically records provider-neutral usage state.
type UsageObservationRepository interface {
	// Record inserts an observation or applies its single pending-to-finalized
	// transition. Exact redelivery is reported as already applied.
	Record(ctx context.Context, observation *UsageObservation) (UsageObservationTransition, error)
	// FindByID restores an observation or returns None when it does not exist.
	FindByID(ctx context.Context, observationID types.UsageObservationID) (types.Optional[*UsageObservation], error)
}
