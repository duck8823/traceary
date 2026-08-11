package types

const maxOrphanConsolidationFailures = 10

// OrphanConsolidationFailure identifies an orphan range that could not be
// consolidated in this pass.
type OrphanConsolidationFailure struct {
	sessionID string
	reason    string
}

// OrphanConsolidationFailureOf creates an OrphanConsolidationFailure.
func OrphanConsolidationFailureOf(sessionID, reason string) OrphanConsolidationFailure {
	return OrphanConsolidationFailure{sessionID: sessionID, reason: reason}
}

// SessionID returns the failed range's session id.
func (f OrphanConsolidationFailure) SessionID() string { return f.sessionID }

// Reason returns why the range could not be consolidated.
func (f OrphanConsolidationFailure) Reason() string { return f.reason }

// OrphanConsolidationFailures retains bounded failure details while preserving
// the total number of skipped candidates.
type OrphanConsolidationFailures struct {
	items []OrphanConsolidationFailure
	total int
}

// OrphanConsolidationFailuresOf creates a bounded failure collection.
func OrphanConsolidationFailuresOf(failures []OrphanConsolidationFailure) OrphanConsolidationFailures {
	collection := OrphanConsolidationFailures{}
	for _, failure := range failures {
		collection = collection.Add(failure)
	}
	return collection
}

// Add records a failure, retaining details only up to the display cap.
func (f OrphanConsolidationFailures) Add(failure OrphanConsolidationFailure) OrphanConsolidationFailures {
	f.total++
	if len(f.items) < maxOrphanConsolidationFailures {
		f.items = append(append([]OrphanConsolidationFailure(nil), f.items...), failure)
	}
	return f
}

// Count returns the total number of skipped candidates.
func (f OrphanConsolidationFailures) Count() int { return f.total }

// Items returns the retained failure details.
func (f OrphanConsolidationFailures) Items() []OrphanConsolidationFailure {
	return append([]OrphanConsolidationFailure(nil), f.items...)
}

// Truncated reports whether some failure details were omitted from Items. The
// cap itself is not exported: a caller that reports the truncation already has
// the retained failures, and len(Items()) is the same number by construction,
// so publishing the constant would only create a second way to be wrong about
// how many were shown.
func (f OrphanConsolidationFailures) Truncated() bool { return f.total > len(f.items) }

// OrphanConsolidationResult is the result of an orphan-range consolidation run.
type OrphanConsolidationResult struct {
	attempted int
	produced  int
	failures  OrphanConsolidationFailures
	hasMore   bool
	dryRun    bool
}

// OrphanConsolidationResultOf creates an OrphanConsolidationResult.
func OrphanConsolidationResultOf(attempted, produced int, failures OrphanConsolidationFailures, hasMore, dryRun bool) OrphanConsolidationResult {
	return OrphanConsolidationResult{
		attempted: attempted,
		produced:  produced,
		failures:  failures,
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
func (r OrphanConsolidationResult) Skipped() int { return r.failures.Count() }

// Failures returns details for skipped candidates.
func (r OrphanConsolidationResult) Failures() OrphanConsolidationFailures { return r.failures }

// HasMore reports that further candidates remain beyond this pass, which is
// the only question that decides whether re-running gc can make progress.
// Skipped candidates are deliberately not folded in here: they will be
// selected and fail again on the next pass, so counting them as unfinished
// work is what made gc ask to be re-run forever. They are reported separately
// instead, with the session and the reason, because that is what the operator
// can actually act on.
//
// This is a statement about this pass, not a proof that nothing unfolded
// remains anywhere. Discovery sees ended or stale sessions plus recorded
// markers, so a session that has stayed continuously active for longer than
// the retention window still holds old unfolded events. Those keep their
// bodies rather than losing them, but they also never become discardable; that
// gap predates bounding and is tracked in #1724.
//
// There is deliberately no Complete() beside this. It used to mean "no more
// candidates and nothing was skipped", and conflating those two facts is the
// whole of #1795; now that it would mean exactly !HasMore(), a second name for
// the same bit would only invite the same conflation back.
func (r OrphanConsolidationResult) HasMore() bool { return r.hasMore }

// DryRun reports whether the run was a dry run.
func (r OrphanConsolidationResult) DryRun() bool { return r.dryRun }
