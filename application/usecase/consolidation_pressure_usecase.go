package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// ConsolidationPolicy is the work-based stop-hook trigger. Either field <= 0 disables.
type ConsolidationPolicy struct {
	MinCommands int64
	StopCadence int64
}

// ConsolidationPressureResult is the read-only work-based trigger outcome.
// PreviousSummary and PreviousCoversTo are set together when a refinement row
// already exists; they travel in the stop-hook reason so the agent can write a
// merged refinement rather than a fresh one.
type ConsolidationPressureResult struct {
	Due              bool
	Signal           string
	Commands         int64
	Stops            int64
	Skipped          string
	PreviousSummary  types.Optional[string]
	PreviousCoversTo types.Optional[types.EventID]
}

// ConsolidationPressureUsecase is the read-only stop-hook work-based check.
type ConsolidationPressureUsecase interface {
	// Check measures work (command_executed since covers_to) and cadence
	// (transcripts since the last request) against policy. Either policy
	// field <= 0 disables the trigger (Due is always false, no I/O).
	Check(ctx context.Context, sessionID types.SessionID, policy ConsolidationPolicy) (ConsolidationPressureResult, error)
}
