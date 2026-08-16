package types

import "time"

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
	attempted                  int
	produced                   int
	failures                   OrphanConsolidationFailures
	hasMore                    bool
	dryRun                     bool
	earliestUnprocessEventTime *time.Time
	earliestSkippedEventTime   *time.Time
}

// OrphanConsolidationResultOf creates an OrphanConsolidationResult.
//
// earliestUnprocessedEventTime is the earliest event time among candidates
// this pass never attempted (nil when hasMore is false, since there is
// nothing left unprocessed; nil while hasMore is true means the time could
// not be determined and callers must fail closed, per #1721).
// earliestSkippedEventTime is the same for candidates that were attempted and
// failed (nil when nothing was skipped).
func OrphanConsolidationResultOf(
	attempted, produced int,
	failures OrphanConsolidationFailures,
	hasMore, dryRun bool,
	earliestUnprocessedEventTime, earliestSkippedEventTime *time.Time,
) OrphanConsolidationResult {
	return OrphanConsolidationResult{
		attempted:                  attempted,
		produced:                   produced,
		failures:                   failures,
		hasMore:                    hasMore,
		dryRun:                     dryRun,
		earliestUnprocessEventTime: earliestUnprocessedEventTime,
		earliestSkippedEventTime:   earliestSkippedEventTime,
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
// remains anywhere. Discovery sees ended or stale sessions, recorded markers,
// and (when the caller supplies a retention cutoff) sessions that never go
// ended or stale but still hold material past coverage older than that
// cutoff. The last of these closes what used to be a standing gap: a session
// that stayed continuously active for longer than the retention window used
// to be invisible to every discovery source, so its old material was never
// folded no matter how old it got. It is now discovered and folded up to,
// but never past, the supplied cutoff — the still-recent tail is left
// uncovered and surfaces again on a later pass once it too ages past cutoff.
//
// There is deliberately no Complete() beside this. It used to mean "no more
// candidates and nothing was skipped", and conflating those two facts is the
// whole of #1795; now that it would mean exactly !HasMore(), a second name for
// the same bit would only invite the same conflation back.
func (r OrphanConsolidationResult) HasMore() bool { return r.hasMore }

// DryRun reports whether the run was a dry run.
func (r OrphanConsolidationResult) DryRun() bool { return r.dryRun }

// EarliestUnprocessedEventTime returns the earliest event time among
// candidates this pass never attempted, or nil when there were none (HasMore
// is false) or the time could not be determined. A caller deciding whether
// deletion up to some cutoff is safe must treat nil while HasMore is true as
// unknown and refuse (#1721).
func (r OrphanConsolidationResult) EarliestUnprocessedEventTime() *time.Time {
	return r.earliestUnprocessEventTime
}

// EarliestSkippedEventTime returns the earliest event time among candidates
// that were attempted and failed, or nil when nothing was skipped or the time
// could not be determined. A caller deciding whether deletion up to some
// cutoff is safe must treat nil while Skipped() > 0 as unknown and refuse
// (#1721).
func (r OrphanConsolidationResult) EarliestSkippedEventTime() *time.Time {
	return r.earliestSkippedEventTime
}
