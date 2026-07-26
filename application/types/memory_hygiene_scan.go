package types

import (
	"time"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	defaultMemoryHygieneMaxRows        = 2_000
	defaultMemoryHygieneMaxScanBytes   = 4 * 1024 * 1024
	defaultMemoryHygieneMaxResultBytes = 256 * 1024
	defaultMemoryHygieneMaxComparisons = 20_000
	defaultMemoryHygieneMaxDuration    = 2 * time.Second
)

// MemoryHygieneScanBudgetParams is the external representation used to build a
// finite per-invocation hygiene budget. A completely zero value selects the
// finite defaults; otherwise every field must be positive.
type MemoryHygieneScanBudgetParams struct {
	MaxRows        int
	MaxScanBytes   int64
	MaxResultBytes int64
	MaxComparisons int
	MaxDuration    time.Duration
}

// MemoryHygieneScanBudget owns the five hard ceilings for one scan invocation.
// Its fields are private so an invalid or accidentally unlimited budget cannot
// cross the application boundary.
type MemoryHygieneScanBudget struct {
	maxRows        int
	maxScanBytes   int64
	maxResultBytes int64
	maxComparisons int
	maxDuration    time.Duration
}

// MemoryHygieneScanBudgetFrom validates explicit limits or resolves the
// completely zero value to finite defaults.
func MemoryHygieneScanBudgetFrom(params MemoryHygieneScanBudgetParams) (MemoryHygieneScanBudget, error) {
	if params == (MemoryHygieneScanBudgetParams{}) {
		return DefaultMemoryHygieneScanBudget(), nil
	}
	if params.MaxRows < 2 {
		return MemoryHygieneScanBudget{}, xerrors.Errorf("memory hygiene max rows must be greater than or equal to 2")
	}
	if params.MaxScanBytes < 1 {
		return MemoryHygieneScanBudget{}, xerrors.Errorf("memory hygiene max scan bytes must be greater than or equal to 1")
	}
	if params.MaxResultBytes < 1 {
		return MemoryHygieneScanBudget{}, xerrors.Errorf("memory hygiene max result bytes must be greater than or equal to 1")
	}
	if params.MaxComparisons < 1 {
		return MemoryHygieneScanBudget{}, xerrors.Errorf("memory hygiene max comparisons must be greater than or equal to 1")
	}
	if params.MaxDuration <= 0 {
		return MemoryHygieneScanBudget{}, xerrors.Errorf("memory hygiene max duration must be greater than 0")
	}
	return MemoryHygieneScanBudget{
		maxRows:        params.MaxRows,
		maxScanBytes:   params.MaxScanBytes,
		maxResultBytes: params.MaxResultBytes,
		maxComparisons: params.MaxComparisons,
		maxDuration:    params.MaxDuration,
	}, nil
}

// DefaultMemoryHygieneScanBudget returns the finite operator defaults.
func DefaultMemoryHygieneScanBudget() MemoryHygieneScanBudget {
	return MemoryHygieneScanBudget{
		maxRows:        defaultMemoryHygieneMaxRows,
		maxScanBytes:   defaultMemoryHygieneMaxScanBytes,
		maxResultBytes: defaultMemoryHygieneMaxResultBytes,
		maxComparisons: defaultMemoryHygieneMaxComparisons,
		maxDuration:    defaultMemoryHygieneMaxDuration,
	}
}

// MaxRows returns the source-row ceiling for one invocation.
func (b MemoryHygieneScanBudget) MaxRows() int { return b.maxRows }

// MaxScanBytes returns the raw source-byte ceiling for one invocation.
func (b MemoryHygieneScanBudget) MaxScanBytes() int64 { return b.maxScanBytes }

// MaxResultBytes returns the serialized suggestion-byte ceiling.
func (b MemoryHygieneScanBudget) MaxResultBytes() int64 { return b.maxResultBytes }

// MaxComparisons returns the duplicate/similarity comparison ceiling.
func (b MemoryHygieneScanBudget) MaxComparisons() int { return b.maxComparisons }

// MaxDuration returns the wall-clock ceiling for one invocation.
func (b MemoryHygieneScanBudget) MaxDuration() time.Duration { return b.maxDuration }

// IsZero reports whether a caller left the budget unset.
func (b MemoryHygieneScanBudget) IsZero() bool {
	return b == (MemoryHygieneScanBudget{})
}

// MemoryHygieneScanPhase is the stable traversal phase encoded in cursors and
// consumed by the SQLite scan source.
type MemoryHygieneScanPhase string

const (
	// MemoryHygieneScanPhaseAcceptedRows starts the accepted-memory row pass.
	MemoryHygieneScanPhaseAcceptedRows MemoryHygieneScanPhase = "accepted_rows"
	// MemoryHygieneScanPhaseExactDuplicates traverses accepted rows for exact peers.
	MemoryHygieneScanPhaseExactDuplicates MemoryHygieneScanPhase = "exact_duplicates"
	// MemoryHygieneScanPhaseSimilarityPairs traverses every unordered same-scope pair.
	MemoryHygieneScanPhaseSimilarityPairs MemoryHygieneScanPhase = "similarity_pairs"
	// MemoryHygieneScanPhaseCandidateRows traverses candidate-noise rows.
	MemoryHygieneScanPhaseCandidateRows MemoryHygieneScanPhase = "candidate_rows"
)

// IsKnown reports whether the phase can be resumed by this build.
func (p MemoryHygieneScanPhase) IsKnown() bool {
	switch p {
	case MemoryHygieneScanPhaseAcceptedRows,
		MemoryHygieneScanPhaseExactDuplicates,
		MemoryHygieneScanPhaseSimilarityPairs,
		MemoryHygieneScanPhaseCandidateRows:
		return true
	default:
		return false
	}
}

// MemoryHygieneScanKeyset contains identities only. Similarity traversal keeps
// the current anchor and last completed partner so every unordered pair can be
// resumed without putting fact text in the cursor.
type MemoryHygieneScanKeyset struct {
	AfterMemoryID  string
	AnchorMemoryID string
	AfterPartnerID string
}

// MemoryHygieneScanPageCriteria is the consumer-oriented bounded request passed
// to the persistence scan source.
type MemoryHygieneScanPageCriteria struct {
	Phase                   MemoryHygieneScanPhase
	Keyset                  MemoryHygieneScanKeyset
	Scopes                  []domtypes.MemoryScope
	IncludeHiddenCandidates bool
	ExpectedRevision        domtypes.Optional[int64]
	MaxRows                 int
	MaxScanBytes            int64
	MaxComparisons          int
}

// MemoryHygieneScanUnit is one cursor-advancing source unit. Peer is populated
// only by the similarity phase; RelatedMemoryID is populated only by the exact
// duplicate phase.
type MemoryHygieneScanUnit struct {
	Row             MemorySummary
	Peer            domtypes.Optional[MemorySummary]
	RelatedMemoryID domtypes.Optional[domtypes.MemoryID]
	NextKeyset      MemoryHygieneScanKeyset
}

// MemoryHygieneScanSourcePage is a revision-consistent bounded page. The source
// accounts raw fact bytes before returning them to the use case.
type MemoryHygieneScanSourcePage struct {
	Revision       int64
	Units          []MemoryHygieneScanUnit
	ProgressKeyset MemoryHygieneScanKeyset
	Done           bool
	StopReason     MemoryHygieneStopReason
	ScannedRows    int
	ScannedBytes   int64
	Comparisons    int
}

// MemoryHygieneRevalidationCriteria scopes the apply path to a requested
// memory and its relevant peers. It has finite row, byte, and comparison
// ceilings.
type MemoryHygieneRevalidationCriteria struct {
	MemoryID                domtypes.MemoryID
	IncludeHiddenCandidates bool
	MaxRows                 int
	MaxScanBytes            int64
	MaxComparisons          int
}

// MemoryHygieneRevalidationSourceResult contains the current target and every
// same-scope peer inspected for targeted apply. Complete=false must fail apply
// closed; it is never interpreted as "no suggestion".
type MemoryHygieneRevalidationSourceResult struct {
	Revision               int64
	Target                 MemorySummary
	ExactDuplicateMemoryID domtypes.Optional[domtypes.MemoryID]
	Peers                  []MemorySummary
	Complete               bool
	StopReason             MemoryHygieneStopReason
	ScannedRows            int
	ScannedBytes           int64
	Comparisons            int
}

// MemoryHygieneStopReason explains why one invocation stopped.
type MemoryHygieneStopReason string

const (
	// MemoryHygieneStopReasonComplete means every scan phase finished.
	MemoryHygieneStopReasonComplete MemoryHygieneStopReason = "complete"
	// MemoryHygieneStopReasonRowLimit means the source-row ceiling stopped the invocation.
	MemoryHygieneStopReasonRowLimit MemoryHygieneStopReason = "row_limit"
	// MemoryHygieneStopReasonScanByteLimit means the raw source-byte ceiling stopped the invocation.
	MemoryHygieneStopReasonScanByteLimit MemoryHygieneStopReason = "scan_byte_limit"
	// MemoryHygieneStopReasonResultByteLimit means the suggestion-byte ceiling stopped the invocation.
	MemoryHygieneStopReasonResultByteLimit MemoryHygieneStopReason = "result_byte_limit"
	// MemoryHygieneStopReasonComparisonLimit means the comparison ceiling stopped the invocation.
	MemoryHygieneStopReasonComparisonLimit MemoryHygieneStopReason = "comparison_limit"
	// MemoryHygieneStopReasonTimeLimit means the wall-clock ceiling stopped the invocation.
	MemoryHygieneStopReasonTimeLimit MemoryHygieneStopReason = "time_limit"
)

// MemoryHygieneScanUsage reports actual work charged to one invocation.
type MemoryHygieneScanUsage struct {
	ScannedRows   int           `json:"scanned_rows"`
	ScannedBytes  int64         `json:"scanned_bytes"`
	ResultBytes   int64         `json:"result_bytes"`
	Comparisons   int           `json:"comparisons"`
	Elapsed       time.Duration `json:"-"`
	ElapsedMillis int64         `json:"elapsed_ms"`
}
