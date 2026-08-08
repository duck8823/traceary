package types

// OrphanConsolidationResult is the result of an orphan-range consolidation run.
type OrphanConsolidationResult struct {
	attempted int
	produced  int
	skipped   int
	hasMore   bool
	dryRun    bool
}

// OrphanConsolidationResultOf creates an OrphanConsolidationResult.
func OrphanConsolidationResultOf(attempted, produced, skipped int, hasMore, dryRun bool) OrphanConsolidationResult {
	return OrphanConsolidationResult{
		attempted: attempted,
		produced:  produced,
		skipped:   skipped,
		hasMore:   hasMore,
		dryRun:    dryRun,
	}
}

// Attempted returns how many candidates the pass tried to process.
func (r OrphanConsolidationResult) Attempted() int { return r.attempted }

// ProducedCount returns how many degraded refinements were written, or would
// be written on a dry run.
func (r OrphanConsolidationResult) ProducedCount() int { return r.produced }

// Skipped returns how many candidates failed and were skipped.
func (r OrphanConsolidationResult) Skipped() int { return r.skipped }

// HasMore reports that further candidates remain beyond this pass.
func (r OrphanConsolidationResult) HasMore() bool { return r.hasMore }

// DryRun reports whether the run was a dry run.
func (r OrphanConsolidationResult) DryRun() bool { return r.dryRun }

// Complete reports that the pass folded every candidate it could see. Only a
// complete pass permits deletion: a skipped or undiscovered range is one whose
// events would be deleted with nothing summarising them.
func (r OrphanConsolidationResult) Complete() bool {
	return !r.hasMore && r.skipped == 0
}
