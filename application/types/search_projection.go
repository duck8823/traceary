//nolint:revive // Public projection API names are intentionally explicit.
package types

import (
	"fmt"
	"time"
)

// DefaultSearchProjectionIndexFamilyBytes is the whole bounded search index
// family — documents, trigram index, session tier and literal fingerprints —
// measured as active b-tree allocation, not source text.
//
// 1464 MiB is what the 4 GiB store gate (#1620) leaves once every other Wave 3
// removal is applied; it is derived, not chosen.
//
// What it buys is a *variable* window, not a fixed one. Trigram measures 2.16x
// the source text, so this is roughly 0.66 GiB of indexable text. Measured
// weekly volume on the reference corpus varies eightfold (0.06 to 0.47 GiB per
// week), which is 1.5 to 2 weeks at the median rate, under a week during a
// heavy sprint and four to five weeks during a quiet one.
//
// Compression (#1685, #1742) buys losslessness, not reach: the index is built
// over plaintext, so a compressed body occupies exactly as much index as an
// uncompressed one. Everything older than the window stays reachable through
// the session tier, which is the design's answer to a short recent window.
const DefaultSearchProjectionIndexFamilyBytes int64 = 1464 << 20

// DefaultSearchProjectionLockTime is the maximum write-lock duration used by
// the CLI and automatic catch-up path for one projection batch.
const DefaultSearchProjectionLockTime = 250 * time.Millisecond

// SearchProjectionCapacitySemanticsVersion is the capacity model this binary
// builds under. A persisted generation below this value is obsolete and must
// be replaced even when complete (#1679 / D5).
const SearchProjectionCapacitySemanticsVersion = 2

type SearchProjectionBudget struct {
	Rows                      int
	WallTime, LockTime        time.Duration
	StoredBytes, DecodedBytes int64
	// WriteBytes is a strict logical mutation-byte budget. It is not a
	// physical database, page, journal, or WAL growth bound.
	WriteBytes int64
	RecentAge  time.Duration
	// IndexFamilyBytes is the operator-facing ceiling on physical bytes of
	// the bounded search index family (active b-tree allocation via dbstat),
	// not source text. Eviction compares against the derived source ceiling,
	// never against this figure directly.
	IndexFamilyBytes int64
}

func (b SearchProjectionBudget) Valid() bool {
	return b.Rows > 0 && b.WallTime > 0 && b.LockTime > 0 && b.StoredBytes > 0 && b.DecodedBytes > 0 && b.WriteBytes > 0 && b.RecentAge > 0 && b.IndexFamilyBytes > 0
}

// ConfigHash contains only budgets that define generation contents. RecentAge
// and IndexFamilyBytes decide what the generation retains, so resuming with a
// different value would leave an index that no single budget describes.
//
// Rows, WallTime, LockTime, StoredBytes and WriteBytes only bound one batch's
// working set, so they stay out of the generation identity: a batch that cannot
// fit its lock cap must be allowed to resume with a smaller unit of work rather
// than discard durable progress. StoredBytes and WriteBytes can reject an
// oversized row, but rejection fails the generation outright — it never quietly
// admits a different set of rows — so they change no content either.
//
// DecodedBytes is held in by choice, not by that argument. It rejects rows the
// same way, and #1794 decides what an over-budget row should do instead; until
// that lands, keeping it here means a widened budget starts a new generation
// rather than silently changing the meaning of a partial one.
func (b SearchProjectionBudget) ConfigHash() string {
	return fmt.Sprintf("v3:%d:%d:%d", b.DecodedBytes, b.RecentAge.Nanoseconds(), b.IndexFamilyBytes)
}

type SearchProjectionGeneration struct {
	GenerationID   string `json:"generation_id"`
	ConfigHash     string `json:"config_hash"`
	SourceRevision int64  `json:"source_revision"`
	HighWater      int64  `json:"high_water"`
	Checkpoint     int64  `json:"checkpoint"`
}
type SearchProjectionProgress struct {
	Selected     int    `json:"selected"`
	Written      int    `json:"written"`
	Evicted      int    `json:"evicted"`
	Cleaned      int    `json:"cleaned"`
	StoredBytes  int64  `json:"stored_bytes"`
	DecodedBytes int64  `json:"decoded_bytes"`
	WrittenBytes int64  `json:"written_bytes"`
	CleanupBytes int64  `json:"cleanup_bytes"`
	Completed    bool   `json:"completed"`
	GenerationID string `json:"generation_id"`
}

// SearchProjectionInventoryItem is a canonical identity admitted by the
// explicit historical inventory phase. EventID is also the stable keyset.
type SearchProjectionInventoryItem struct {
	EventID      string
	LogicalBytes int64
	Missing      bool
}

type SearchProjectionInventorySnapshot struct {
	Generation    SearchProjectionGeneration
	Cursor        string
	CursorStarted bool
	Items         []SearchProjectionInventoryItem
	Done          bool
}

type SearchProjectionInventoryPlan struct {
	GenerationID, ExpectedCursor, NextCursor string
	ExpectedCursorStarted, NextCursorStarted bool
	ExpectedRevision                         int64
	Items                                    []SearchProjectionInventoryItem
	Done                                     bool
	Ledger                                   BudgetLedger
}

type SearchProjectionRunOptions struct {
	MaxBatches    int
	TotalWallTime time.Duration
}
type SearchProjectionRunResult struct {
	Batches             int                      `json:"batches"`
	Progress            SearchProjectionProgress `json:"progress"`
	StopReason          string                   `json:"stop_reason"`
	ElapsedMilliseconds int64                    `json:"elapsed_milliseconds"`
}
type SearchProjectionAbandonResult struct {
	GenerationID     string `json:"generation_id"`
	State            string `json:"state"`
	AlreadyAbandoned bool   `json:"already_abandoned"`
}

// ProjectionDocument is canonical, hydrated input. It deliberately contains no
// SQLite identity or DTO; Sequence is the stable application checkpoint.
type ProjectionDocument struct {
	Sequence                                             int64
	EventID, SessionID, CreatedAt, Text, PreviousSummary string
	StoredBytes, DecodedBytes                            int64
	Deleted                                              bool
	CommandCount, FailureCount                           int
	Disposition                                           ProjectionDisposition
}

// ProjectionDisposition distinguishes a deliberately unindexed source row
// from a deleted row that merely advances the checkpoint.
type ProjectionDisposition string

const (
	ProjectionDispositionAdmitted  ProjectionDisposition = "admitted"
	ProjectionDispositionDeleted   ProjectionDisposition = "deleted"
	ProjectionDispositionExcluded  ProjectionDisposition = "excluded"
	ProjectionDispositionBatchFull ProjectionDisposition = ""
)

type ProjectionSnapshot struct {
	Generation    SearchProjectionGeneration
	Phase         string
	Documents     []ProjectionDocument
	SourceDone    bool
	RetainedBytes int64
	Cleanup       []ProjectionCleanupCandidate
	CleanupDone   bool
	CleanupAll    bool
	Now           time.Time
	// RecentCutoffNorm is the source-phase prefilter cutoff derived at Start
	// from the index-family budget. Empty means age-only retention — the whole
	// corpus fits under the walk ceiling. A far-future timestamp means the
	// opposite: the derived ceiling is 0, so nothing qualifies and the source
	// phase must build nothing rather than build everything and evict it. The
	// pure planner combines this with RecentAge; it never learns about dbstat.
	RecentCutoffNorm string
	// RecentSourceCeilingBytes is the persisted source-text ceiling eviction
	// compares against. Zero means the entire recent tier is over budget and
	// must be emptied: the eviction predicate is
	// total_bytes - removed_before > ceiling, which is true for every row
	// when the ceiling is 0. Age-only retention is an empty RecentCutoffNorm
	// with a positive ceiling (or no ceiling column at all on pre-v2 stores).
	RecentSourceCeilingBytes int64
}

type ProjectionCleanupCandidate struct {
	Class               string
	RowID, LogicalBytes int64
}

type ProjectionWrite struct {
	Document            ProjectionDocument
	Summary             string
	Keywords            map[string]int
	LogicalBytes        int64
	RetainRecent        bool
	LiteralFingerprints []string
}

type ProjectionExclusion struct {
	Sequence, MeasuredBytes, ByteLimit int64
	EventID, Class                     string
}

// ProjectionBatchPlan is a pure application-owned decision. Adapters may only
// persist this decision; they must not extend it with additional rows.
type ProjectionBatchPlan struct {
	GenerationID, Phase                                  string
	ExpectedRevision, ExpectedCheckpoint, NextCheckpoint int64
	Writes                                               []ProjectionWrite
	Exclusions                                           []ProjectionExclusion
	Cleanup                                              []ProjectionCleanupCandidate
	NextPhase                                            string
	Completed                                            bool
	FinalState                                           string
	AllowRevisionDrift                                   bool
	ContinueState                                        string
	Ledger                                               BudgetLedger
}

// BudgetLedger accounts source reads and strict logical mutation bytes.
type BudgetLedger struct {
	Rows                                         int
	StoredBytes, DecodedBytes, LogicalWriteBytes int64
}

// ReserveSource is the application-owned hard-cap rule used before hydration.
func (l *BudgetLedger) ReserveSource(b SearchProjectionBudget, stored, decoded int64) bool {
	if l.Rows >= b.Rows || l.StoredBytes+stored > b.StoredBytes || l.DecodedBytes+decoded > b.DecodedBytes {
		return false
	}
	l.Rows++
	l.StoredBytes += stored
	l.DecodedBytes += decoded
	return true
}

// AdmitSource returns a disposition without hydrating an over-limit source row.
func (l *BudgetLedger) AdmitSource(b SearchProjectionBudget, stored, decoded int64) (ProjectionDisposition, error) {
	if stored > b.StoredBytes {
		return ProjectionDispositionExcluded, nil
	}
	if decoded > b.DecodedBytes {
		return ProjectionDispositionExcluded, nil
	}
	if !l.ReserveSource(b, stored, decoded) {
		return ProjectionDispositionBatchFull, nil
	}
	return ProjectionDispositionAdmitted, nil
}

// RetentionPlan is the pure eviction/old-generation cleanup decision.
type ProjectionRetentionPlan struct {
	Candidates []ProjectionCleanupCandidate
	Ledger     BudgetLedger
}

type SearchProjectionOversizeError struct {
	Class        string
	Bytes, Limit int64
}

func (e *SearchProjectionOversizeError) Error() string {
	return fmt.Sprintf("search projection %s exceeds batch budget (%d > %d)", e.Class, e.Bytes, e.Limit)
}

type SearchProjectionNoProgressCode string

const (
	SearchProjectionNoProgressLockDurationCap          SearchProjectionNoProgressCode = "lock_duration_cap_exceeded"
	SearchProjectionNoProgressSingleRowLockDurationCap SearchProjectionNoProgressCode = "single_row_lock_duration_cap_exceeded"
)

type SearchProjectionNoProgressError struct {
	Code   SearchProjectionNoProgressCode
	Reason string
}

func (e *SearchProjectionNoProgressError) Error() string {
	return "search projection made no progress: " + e.Reason
}

type SearchProjectionDriftError struct{}

func (*SearchProjectionDriftError) Error() string {
	return "search projection source changed; start a new generation"
}

type SearchProjectionStatus struct {
	SchemaVersion           string `json:"schema_version"`
	State                   string `json:"state"`
	Phase                   string `json:"phase"`
	ProjectionVersion       int    `json:"projection_version"`
	FTSDesign               string `json:"fts_design"`
	ConfigHash              string `json:"config_hash"`
	SourceRevision          int64  `json:"source_revision"`
	HighWater               int64  `json:"high_water"`
	Checkpoint              int64  `json:"checkpoint"`
	Completed               bool   `json:"completed"`
	RecentAgeSeconds        int64  `json:"recent_age_seconds"`
	// IndexFamilyByteLimit is the configured physical-byte budget for the
	// bounded search index family (the unit the operator sets).
	IndexFamilyByteLimit int64 `json:"index_family_byte_limit"`
	// RecentBytes is source text actually retained in the recent tier — a
	// different unit from IndexFamilyByteLimit, which is the point of #1679.
	RecentBytes                 int64            `json:"recent_bytes"`
	RecentDocuments             int64            `json:"recent_documents"`
	RecentSourceCeilingBytes    int64            `json:"recent_source_ceiling_bytes"`
	RecentAmplificationPPM      int64            `json:"recent_amplification_ppm"`
	NonRecentFamilyBytes        int64            `json:"non_recent_family_bytes"`
	RecentCutoffNorm            string           `json:"recent_cutoff_norm,omitempty"`
	CapacitySemanticsVersion    int              `json:"capacity_semantics_version"`
	CapacityEvidence            CapacityEvidence `json:"capacity_evidence"`
	IndexFamilyWithinBudget     int              `json:"index_family_within_budget"`
	SummarySessions             int64            `json:"summary_sessions"`
	KeywordRows                 int64            `json:"keyword_rows"`
	SummaryLogicalBytes         int64            `json:"summary_logical_bytes"`
	KeywordLogicalBytes         int64            `json:"keyword_logical_bytes"`
	// FTSLogicalBytes is the logical byte total of the FTS5 shadow tables,
	// distinct from RecentBytes, which is source-text bytes.
	FTSLogicalBytes             int64            `json:"fts_logical_bytes"`
	PhysicalBytes               int64            `json:"physical_bytes"`
	PhysicalEvidence            CapacityEvidence `json:"physical_evidence"`
	LastBatchMilliseconds       int64            `json:"last_batch_milliseconds"`
	InspectionMilliseconds      int64            `json:"inspection_milliseconds"`
	MatchProbeMilliseconds      int64            `json:"match_probe_milliseconds"`
	KeywordVersion              int              `json:"keyword_version"`
	FingerprintVersion          int              `json:"fingerprint_version"`
	FingerprintRows             int64            `json:"fingerprint_rows"`
	FingerprintLogicalBytes     int64            `json:"fingerprint_logical_bytes"`
	LifecycleState              string           `json:"lifecycle_state"`
	AbandonedAt                 string           `json:"abandoned_at,omitempty"`
	// FailureClass names why the last generation failed. It is what decides
	// whether automatic catch-up may start a replacement: a deterministic class
	// would fail the same way on every open.
	FailureClass string `json:"failure_class,omitempty"`
	ExclusionCount int64 `json:"exclusion_count"`
	Exclusions []SearchProjectionExclusion `json:"exclusions,omitempty"`
	// CutoverIndexFamily names which physical family CutoverFamilyBytes*
	// measure. Always "bounded_search_projection" when set — never the
	// legacy migration-032 event_search_* family (that is #1718).
	CutoverIndexFamily       string `json:"cutover_index_family,omitempty"`
	CutoverFamilyBytesBefore int64  `json:"cutover_family_bytes_before,omitempty"`
	CutoverFamilyBytesAfter  int64  `json:"cutover_family_bytes_after,omitempty"`
	// CutoverBeforeEvidence and CutoverAfterEvidence state whether each byte
	// figure above was actually measured. Without them a family that could not
	// be measured reports zero bytes, which reads identically to a genuinely
	// empty family. They are separate because the two walks happen at different
	// times against families of different sizes: one can succeed while the
	// other times out. An empty Status means no measurement has been attempted
	// yet.
	CutoverBeforeEvidence CapacityEvidence `json:"cutover_before_evidence"`
	CutoverAfterEvidence  CapacityEvidence `json:"cutover_after_evidence"`
}

type SearchProjectionExclusion struct {
	Sequence int64 `json:"sequence"`
	EventID string `json:"event_id"`
	Class string `json:"class"`
	MeasuredBytes int64 `json:"measured_bytes"`
	ByteLimit int64 `json:"byte_limit"`
}

// SearchProjectionControlStatus contains only persisted state-machine data.
// It deliberately excludes derived measurements so lifecycle operations cannot
// accidentally put whole-family scans on their control path.
type SearchProjectionControlStatus struct {
	State                    string
	Phase                    string
	ConfigHash               string
	CapacitySemanticsVersion int
	FailureClass             string
	CutoverIndexFamily       string
	CutoverFamilyBytesBefore int64
	CutoverFamilyBytesAfter  int64
	CutoverBeforeEvidence    CapacityEvidence
	CutoverAfterEvidence     CapacityEvidence
}

// SearchProjectionCatchUpResult is one bounded unit of automatic generation
// work performed during store initialization. It mirrors the event-search
// backfill shape: a single open does a bounded amount of work and resumes
// later without operator action.
type SearchProjectionCatchUpResult struct {
	Action                   string `json:"action"`
	State                    string `json:"state"`
	Phase                    string `json:"phase"`
	GenerationID             string `json:"generation_id,omitempty"`
	Completed                bool   `json:"completed"`
	Batches                  int    `json:"batches"`
	Selected                 int    `json:"selected"`
	Written                  int    `json:"written"`
	SkippedReason            string `json:"skipped_reason,omitempty"`
	CutoverIndexFamily       string `json:"cutover_index_family,omitempty"`
	CutoverFamilyBytesBefore int64  `json:"cutover_family_bytes_before,omitempty"`
	CutoverFamilyBytesAfter  int64  `json:"cutover_family_bytes_after,omitempty"`
	// CutoverBeforeEvidence and CutoverAfterEvidence carry the same
	// measured/unavailable distinction as SearchProjectionStatus so a zero in
	// the byte fields above is never read as an empty family when it only means
	// the walk did not run.
	CutoverBeforeEvidence CapacityEvidence `json:"cutover_before_evidence"`
	CutoverAfterEvidence  CapacityEvidence `json:"cutover_after_evidence"`
	SessionTierVerified   bool             `json:"session_tier_verified,omitempty"`
}
