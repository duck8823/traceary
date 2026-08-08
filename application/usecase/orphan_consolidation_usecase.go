package usecase

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// OrphanConsolidationInput configures a consolidation pass.
type OrphanConsolidationInput struct {
	// StaleAfter is the inactivity window used to treat a session as ended.
	// Must match the existing session-gc stale rule (default 24h).
	StaleAfter time.Duration
	// DryRun counts candidates without writing refinements.
	DryRun bool
}

// OrphanConsolidationUsecase folds unfolded orphan ranges into degraded
// mechanical summaries. It is deliberately separate from store-management
// CollectGarbage, whose contract is "delete and report a count".
type OrphanConsolidationUsecase interface {
	// Consolidate discovers orphan ranges and produces degraded refinements.
	// On DryRun it counts candidates and writes nothing.
	Consolidate(ctx context.Context, input OrphanConsolidationInput) (apptypes.OrphanConsolidationResult, error)
}
