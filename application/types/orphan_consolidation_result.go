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

// Complete reports that the pass folded every candidate discovery returned. It
// gates deletion: a pass that stopped at its bound or skipped a range leaves
// events unfolded that the retention cutoff would then remove.
//
// It is a statement about this pass, not a proof that nothing unfolded remains
// anywhere. Discovery sees ended or stale sessions plus recorded markers, so a
// session that has stayed continuously active for longer than the retention
// window still holds old unfolded events deletion can reach. That gap predates
// bounding and is tracked in #1724; do not read Complete() as covering it.
func (r OrphanConsolidationResult) Complete() bool {
	return !r.hasMore && r.skipped == 0
}
