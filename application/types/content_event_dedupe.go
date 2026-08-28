package types

import (
	"fmt"
	"time"
)

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
	// MaxScanRows bounds body materialization for diagnostic dry-runs. Zero
	// preserves the maintenance command's full-scan behavior. A positive bound
	// is invalid when Apply is true so cleanup can never mutate a partial view.
	MaxScanRows int
	// RunID identifies the apply run; it is recorded on every archived row so
	// `--restore <run-id>` can reverse exactly this run. Required when Apply is
	// true and ignored otherwise.
	RunID string
	// Now stamps archived_at. Required when Apply is true and ignored otherwise.
	Now time.Time
	// BatchSize bounds how many duplicate rows one apply transaction quarantines.
	// Zero selects DefaultContentEventDedupeBatchSize, which is what every caller
	// does today: the bound is a durability property of the repair, not an
	// operator dial. It stays a parameter so tests can drive the batch boundary
	// directly. Committing per batch is
	// what keeps an interrupted repair consistent and re-runnable; it is not a
	// scan bound (see MaxScanRows, which samples and therefore cannot be applied).
	BatchSize int
}

// DefaultContentEventDedupeBatchSize bounds one apply transaction. The full-store
// repair spans hundreds of thousands of rows, so a single transaction would hold
// the entire archive+delete set before any of it became durable and would lose
// all progress on interruption.
const DefaultContentEventDedupeBatchSize = 1000

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

// ContentEventDedupeSourceStat keeps heuristic candidate measurement separate
// from proven stable-ID redelivery metrics.
type ContentEventDedupeSourceStat struct {
	Agent          string  `json:"agent"`
	SourceHook     string  `json:"source_hook"`
	ScannedCount   int     `json:"scanned_count"`
	GroupCount     int     `json:"group_count"`
	CandidateCount int     `json:"candidate_count"`
	CandidateRate  float64 `json:"candidate_rate"`
}

// ContentEventDedupeResult is the outcome of a dedupe run (dry-run or apply).
type ContentEventDedupeResult struct {
	RunID              string
	Applied            bool
	TotalEligibleCount int
	ScannedCount       int
	Groups             []ContentEventDedupeGroup
	Skipped            []ContentEventDedupeSkip
	Sources            []ContentEventDedupeSourceStat
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

// ContentEventDedupeRun summarizes one quarantine run still held in the archive.
//
// Listing exists because a run id is the only handle on `--restore` and
// `--purge`, and an apply that is interrupted after its first batch commits has
// already quarantined rows under an id the operator never saw printed. Without
// a listing those rows would be unreachable: invisible in `events`, un-restorable
// and un-purgeable.
type ContentEventDedupeRun struct {
	RunID      string
	ArchivedAt string // newest archived_at, raw (lexical MAX; see #1185)
	// OldestArchivedAt is the earliest parseable archived_at in this run, in
	// ts_norm's fixed-width UTC form. Empty when no row in the run has a
	// parseable timestamp.
	OldestArchivedAt string
	QuarantinedRows  int
	// BodyBytes is the total quarantined body length held by this run, in bytes.
	BodyBytes int64
	// Internal is true for quarantine compact minted for its own copy-filter
	// apply (the compact-copy-filter-* namespace). Compact owns those rows; the
	// next replica/external `store compact` drops them.
	Internal bool
}

// ContentEventDedupeApplyError wraps a failure from an apply run (Apply:true)
// so the run id that already-committed batches were archived under is never
// lost. Apply commits in bounded batches (see ContentEventDedupeParams.BatchSize),
// so a run that fails partway through has already quarantined rows durably
// under RunID even though the call returns an error. The result value carrying
// that outcome is discarded by callers on error, so the run id's only surviving
// path out of the failure is this error.
type ContentEventDedupeApplyError struct {
	// RunID is the id every row committed before the failure was archived
	// under, and the id RestoreContentEventDedupeRun needs to recover them.
	RunID string
	// Err is the underlying failure.
	Err error
}

func (e *ContentEventDedupeApplyError) Error() string {
	return fmt.Sprintf("content-event dedupe apply failed for run %s: %v", e.RunID, e.Err)
}

func (e *ContentEventDedupeApplyError) Unwrap() error {
	return e.Err
}

// ContentEventDedupePurgeResult is the outcome of ending a quarantine run's
// rollback window. Until a run is purged its bodies still occupy the store, so
// apply relocates duplicates rather than reclaiming them.
type ContentEventDedupePurgeResult struct {
	RunID       string
	PurgedCount int
	// ReleasedBody is the total quarantined body length dropped, in bytes. SQLite
	// returns the pages to the free list; VACUUM is what returns them to the
	// filesystem.
	ReleasedBody int64
}
