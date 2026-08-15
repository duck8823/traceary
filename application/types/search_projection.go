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
// It is a *target*, not a cap. Only the recent tier can be evicted, so only it
// is enforced: the corpus-proportional tiers (session summaries and keywords,
// literal fingerprints) are measured and subtracted from this figure as a
// reserve, and the remainder becomes the recent tier's source-text ceiling. The
// family total is re-measured when a generation completes and recorded through
// index_family_within_budget — reported, not guaranteed in advance.
//
// It buys retention in the recent tier, not searchable reach: nothing currently
// reads that tier. Trigram measures 2.16x the source text, so this is roughly
// 0.66 GiB of retained indexable text. Measured weekly volume on the reference
// corpus varies eightfold (0.06 to 0.47 GiB per week), which is 1.5 to 2 weeks
// at the median rate, under a week during a heavy sprint and four to five weeks
// during a quiet one.
//
// Compression (#1685, #1742) buys losslessness, not reach: the index is built
// over plaintext, so a compressed body occupies exactly as much index as an
// uncompressed one. The session tier is an additional, lossy surface for
// summary and keyword matches; literal search reach comes from decoding
// candidates in the canonical event walk.
const DefaultSearchProjectionIndexFamilyBytes int64 = 1464 << 20

// DefaultSearchProjectionLockTime is the maximum write-lock duration used by
// the CLI and automatic catch-up path for one projection batch.
const DefaultSearchProjectionLockTime = 250 * time.Millisecond

// DefaultSearchProjectionBudget is the bounded search projection budget used
// by both the CLI flag defaults and the automatic catch-up path, which must
// agree: five of these fields feed ConfigHash, so an operator running
// `store search-projection resume` with default flags is refused the
// generation automatic catch-up started as soon as one of them drifts. The
// refusal reports only "budget does not match generation configuration" — the
// operator cannot see that two defaults disagree, because both look like the
// default. One owner makes that impossible rather than detectable.
//
// Rows is deliberately part of this value but not of ConfigHash: the adaptive
// shrink varies it within a generation, so it cannot identify one.
func DefaultSearchProjectionBudget() SearchProjectionBudget {
	return SearchProjectionBudget{
		Rows:             128,
		WallTime:         time.Second,
		LockTime:         DefaultSearchProjectionLockTime,
		StoredBytes:      8 << 20,
		DecodedBytes:     8 << 20,
		WriteBytes:       8 << 20,
		RecentAge:        30 * 24 * time.Hour,
		IndexFamilyBytes: DefaultSearchProjectionIndexFamilyBytes,
	}
}

// SearchProjectionCapacitySemanticsVersion is the capacity model this binary
// builds under. A persisted generation below this value is obsolete and must
// be replaced even when complete (#1679 / D5).
const SearchProjectionCapacitySemanticsVersion = 2

// SearchProjectionOrigin names who created the live generation. CatchUp may
// replace an automatic generation whose ConfigHash no longer matches the
// current default. It must not replace an operator-owned one (#1861).
const (
	SearchProjectionOriginAutomatic = "automatic"
	SearchProjectionOriginOperator  = "operator"
	SearchProjectionStartCommand    = "traceary store search-projection start"
)

type SearchProjectionBudget struct {
	Rows                      int
	WallTime, LockTime        time.Duration
	StoredBytes, DecodedBytes int64
	// WriteBytes is a strict logical mutation-byte budget. It is not a
	// physical database, page, journal, or WAL growth bound.
	WriteBytes int64
	RecentAge  time.Duration
	// IndexFamilyBytes is the operator-facing budget for physical bytes of
	// the bounded search index family (active b-tree allocation via dbstat),
	// not source text. It is a target rather than a cap: eviction compares
	// against the source ceiling derived from it after the non-recent reserve
	// is subtracted, never against this figure directly, and the family total
	// is reported at completion rather than enforced.
	IndexFamilyBytes int64
}

func (b SearchProjectionBudget) Valid() bool {
	return b.Rows > 0 && b.WallTime > 0 && b.LockTime > 0 && b.StoredBytes > 0 && b.DecodedBytes > 0 && b.WriteBytes > 0 && b.RecentAge > 0 && b.IndexFamilyBytes > 0
}

// ConfigHash is the capacity identity of a generation (#1754).
//
// RecentAge and IndexFamilyBytes decide what the recent tier retains.
// StoredBytes, DecodedBytes and WriteBytes also belong here: a source row
// that exceeds any of them is recorded as a durable exclusion, so changing
// them mid-rebuild would leave an index no single budget describes.
//
// Rows, WallTime and LockTime only bound the cost of one batch (and Rows
// shrinks adaptively inside a generation), so they stay out. Changing those
// flags must resume the same generation from its checkpoint.
func (b SearchProjectionBudget) ConfigHash() string {
	return fmt.Sprintf("v4:%d:%d:%d:%d:%d", b.StoredBytes, b.DecodedBytes, b.WriteBytes, b.RecentAge.Nanoseconds(), b.IndexFamilyBytes)
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
	RecentSourceBytes        int64
}

type ProjectionCleanupCandidate struct {
	Class               string
	RowID, LogicalBytes int64
	ReleasedSourceBytes int64
	CreatedAtNorm       string
	Expired             bool
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
	ExpectedRecentSourceBytes                            int64
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
	// SearchProjectionNoProgressRowWorkCap means the write lock was already
	// held and the row's own work exceeded the hold budget. Acquisition
	// failures stay LockDurationCap so contention is not this code.
	SearchProjectionNoProgressRowWorkCap SearchProjectionNoProgressCode = "row_work_cap_exceeded"
)

type SearchProjectionNoProgressError struct {
	Code   SearchProjectionNoProgressCode
	Reason string
	// Exclusion is set when a single source write exceeded the hold budget so
	// the caller can persist a row_work skip without re-reading the snapshot.
	Exclusion ProjectionExclusion
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
	// RecentSourceBytes is the persisted cache used by interleaved eviction
	// (search_projection_state.recent_source_bytes). It is scoped to
	// generation_id, which during a rebuild is the incoming generation, not
	// the active one RecentBytes sums.
	RecentSourceBytes           int64            `json:"recent_source_bytes"`
	// RecentSourceBytesMeasured is SUM(decoded_bytes) for that same
	// generation_id. Delta is cache minus measured. Status does not rewrite
	// the cache.
	RecentSourceBytesMeasured   int64            `json:"recent_source_bytes_measured"`
	RecentSourceBytesDelta      int64            `json:"recent_source_bytes_delta"`
	RecentSourceBytesEvidence   CapacityEvidence `json:"recent_source_bytes_evidence"`
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
	ExclusionGenerationID string                       `json:"exclusion_generation_id,omitempty"`
	ExclusionCount        int64                        `json:"exclusion_count"`
	Exclusions             []SearchProjectionExclusion `json:"exclusions,omitempty"`
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
	// Origin is automatic (store-open catch-up) or operator (explicit start).
	Origin string `json:"origin,omitempty"`
	// ParkedReason is set when automatic catch-up will not advance this
	// generation. RecoveryCommand names the operator command that replaces it.
	ParkedReason    string `json:"parked_reason,omitempty"`
	RecoveryCommand string `json:"recovery_command,omitempty"`
}

// ApplyParkedNotice fills ParkedReason and RecoveryCommand from persisted
// state so `store search-projection status` can answer why catch-up is stuck.
// defaultConfigHash is the current DefaultSearchProjectionBudget hash.
func (s *SearchProjectionStatus) ApplyParkedNotice(defaultConfigHash string) {
	switch {
	case s.State == "failed":
		class := s.FailureClass
		if class == "" {
			class = "(unclassified)"
		}
		s.ParkedReason = "parked after generation failure " + class
		s.RecoveryCommand = SearchProjectionStartCommand
	case (s.State == "rebuilding" || (s.State == "drifted" && s.Phase == "cleanup")) &&
		s.ConfigHash != "" && s.ConfigHash != defaultConfigHash:
		if s.Origin == SearchProjectionOriginAutomatic {
			s.ParkedReason = "automatic generation budget is stale; the next store open replaces this generation"
			return
		}
		s.ParkedReason = "budget does not match generation configuration"
		s.RecoveryCommand = SearchProjectionStartCommand
	}
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
	GenerationID             string
	State                    string
	Phase                    string
	Checkpoint               int64
	ConfigHash               string
	CapacitySemanticsVersion int
	FailureClass             string
	CutoverIndexFamily       string
	CutoverFamilyBytesBefore int64
	CutoverFamilyBytesAfter  int64
	CutoverBeforeEvidence    CapacityEvidence
	CutoverAfterEvidence     CapacityEvidence
	Origin                   string
}

// SearchProjectionCatchUpResult is one bounded unit of automatic generation
// work performed during store initialization. It mirrors the event-search
// backfill shape: a single open does a bounded amount of work and resumes
// later without operator action.
type SearchProjectionCatchUpResult struct {
	Action                   string `json:"action"`
	State                    string `json:"state"`
	Phase                    string `json:"phase"`
	Checkpoint               int64  `json:"checkpoint"`
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
