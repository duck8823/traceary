package types

import "time"

// ContentEventDedupeParams configures a single content-event dedupe maintenance
// run (`traceary store dedupe content-events`).
//
// The maintenance path targets only historical hook-originated prompt/transcript
// duplicates. Callers select a scope by Agent (the CLI surface exposes this as
// `--client codex`, but duplicates are written with events.client="hook", so the
// store-side filter is by agent). Apply gates the only data-mutating behavior:
// when false the run is a pure dry-run and never writes.
type ContentEventDedupeParams struct {
	// Agent restricts the scan to events with this agent (e.g. "codex"). Empty
	// means every agent participates.
	Agent string
	// Apply moves duplicate rows into the quarantine archive. When false the run
	// reports candidates only and never mutates the store.
	Apply bool
	// Strict reports every exact duplicate group regardless of time gap. The
	// default clusters by the 10s proximity window (mirroring the
	// content-event-reliability doctor check) so only near-simultaneous (likely
	// hook double-write) groups are eligible.
	Strict bool
	// RunID identifies the apply run; it is recorded on every archived row so
	// `--restore <run-id>` can reverse exactly this run. Required when Apply is
	// true and ignored otherwise.
	RunID string
	// Now stamps archived_at. Required when Apply is true and ignored otherwise.
	Now time.Time
}

// ContentEventDedupeGroup is one duplicate group the dedupe run selected. The
// kept row is the canonical survivor (earliest parsed created_at, tie-broken by
// event id); every DuplicateEventID is a row that `--apply` quarantines.
type ContentEventDedupeGroup struct {
	KeptEventID       string
	DuplicateEventIDs []string
	Kind              string
	Agent             string
	SourceHook        string
	// GroupKey is the forensic identity key (kind|client|agent|session|workspace|hook|body-hash).
	GroupKey string
}

// DuplicateCount returns the number of rows that would be (or were) quarantined
// for this group.
func (g ContentEventDedupeGroup) DuplicateCount() int { return len(g.DuplicateEventIDs) }

// ContentEventDedupeSkip records a duplicate group skipped because at least one
// member carried a malformed timestamp or the ordering was otherwise ambiguous,
// so a canonical row could not be chosen safely. Skipped groups are never
// mutated; they are reported for operator follow-up.
type ContentEventDedupeSkip struct {
	GroupKey string
	EventIDs []string
	Reason   string
}

// ContentEventDedupeResult is the outcome of a dedupe run (dry-run or apply).
type ContentEventDedupeResult struct {
	RunID        string
	Applied      bool
	ScannedCount int
	Groups       []ContentEventDedupeGroup
	Skipped      []ContentEventDedupeSkip
}

// MovedCount returns the total number of duplicate rows across all groups (the
// number of rows quarantined on apply, or that would be on dry-run).
func (r ContentEventDedupeResult) MovedCount() int {
	total := 0
	for _, group := range r.Groups {
		total += group.DuplicateCount()
	}
	return total
}

// ContentEventDedupeRestoreResult is the outcome of restoring a quarantine run.
type ContentEventDedupeRestoreResult struct {
	RunID         string
	RestoredCount int
}
