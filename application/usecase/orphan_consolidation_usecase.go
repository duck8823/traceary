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
	// Limit bounds how many candidates one pass discovers. Zero or less uses
	// defaultOrphanConsolidationLimit.
	Limit int
	// Budget bounds how long one pass spends processing candidates. Zero or
	// less uses defaultOrphanConsolidationBudget. Discovery is bounded by
	// Limit; this bounds the loop that follows it.
	Budget time.Duration
	// Unlimited ignores Limit and Budget so compact --force can cover every
	// discardable-age unrefined session in one rewrite.
	Unlimited bool
	// RetentionCutoff enables the third orphan-discovery source (#1724):
	// sessions that never go ended/stale but still hold material past
	// refinement coverage older than this cutoff. The zero value disables
	// that source, keeping discovery to the recorded-marker and
	// ended/stale sources only. Callers that know the compact retention
	// cutoff (e.g. the --force cover pass) should pass it here so an
	// always-on session's pre-cutoff tail is folded before CollectGarbage's
	// DELETE runs.
	RetentionCutoff time.Time
}

// OrphanConsolidationUsecase folds unfolded orphan ranges into degraded
// mechanical summaries. It is deliberately separate from store-management
// CollectGarbage, whose contract is "delete and report a count".
type OrphanConsolidationUsecase interface {
	// Consolidate discovers orphan ranges and produces degraded refinements.
	// On DryRun it counts candidates and writes nothing.
	Consolidate(ctx context.Context, input OrphanConsolidationInput) (apptypes.OrphanConsolidationResult, error)
}
