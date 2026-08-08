package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// ConsolidationPressureResult is the read-only pressure check outcome.
// PreviousSummary and PreviousCoversTo are set together when a refinement row
// already exists; they travel in the stop-hook reason so the agent can write a
// merged refinement rather than a fresh one (#1673 needs no separate read).
type ConsolidationPressureResult struct {
	PressureBytes    int64
	Due              bool
	PreviousSummary  types.Optional[string]
	PreviousCoversTo types.Optional[types.EventID]
}

// ConsolidationPressureUsecase is the read-only stop-hook pressure check.
type ConsolidationPressureUsecase interface {
	// Check measures unrefined body-byte pressure for sessionID against
	// thresholdBytes. thresholdBytes <= 0 means the trigger is disabled
	// (Due is always false; pressure is still measured when possible).
	Check(ctx context.Context, sessionID types.SessionID, thresholdBytes int64) (ConsolidationPressureResult, error)
}
