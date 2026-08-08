package types

// OrphanConsolidationResult is the result of an orphan-range consolidation run.
type OrphanConsolidationResult struct {
	producedCount int
	dryRun        bool
}

// OrphanConsolidationResultOf creates an OrphanConsolidationResult.
func OrphanConsolidationResultOf(producedCount int, dryRun bool) OrphanConsolidationResult {
	return OrphanConsolidationResult{
		producedCount: producedCount,
		dryRun:        dryRun,
	}
}

// ProducedCount returns how many degraded refinements were written, or would
// be written on a dry run.
func (r OrphanConsolidationResult) ProducedCount() int { return r.producedCount }

// DryRun reports whether the run was a dry run.
func (r OrphanConsolidationResult) DryRun() bool { return r.dryRun }
