package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemoryHygieneSuggestionKind names the reason a memory was surfaced by
// the hygiene scan. Keeping the enumeration small means the CLI and MCP
// output can branch on a single field without reimplementing the scanner
// logic on the read side.
type MemoryHygieneSuggestionKind string

const (
	// MemoryHygieneSuggestionRedactionHit flags an accepted memory whose
	// sanitized fact differs from the stored fact, meaning the current
	// audit redaction rules would mask content that the stored row still
	// exposes.
	MemoryHygieneSuggestionRedactionHit MemoryHygieneSuggestionKind = "redaction_hit"
	// MemoryHygieneSuggestionExpiryCandidate flags an accepted memory
	// whose updated_at is older than the hygiene staleness threshold, so
	// an operator may want to expire it.
	MemoryHygieneSuggestionExpiryCandidate MemoryHygieneSuggestionKind = "expiry_candidate"
	// MemoryHygieneSuggestionDuplicate flags two accepted memories that
	// share the same scope and fact text, so one should supersede or
	// expire the other to keep the store tidy.
	MemoryHygieneSuggestionDuplicate MemoryHygieneSuggestionKind = "duplicate"
	// MemoryHygieneSuggestionSupersedeCandidate flags two accepted
	// memories that share a scope and a meaningful word overlap (word
	// Jaccard >= defaultSupersedeSimilarityThreshold) but carry
	// different text. The newer memory is suggested as the replacement;
	// the CLI apply path supersedes the older memory with the newer
	// memory's content so the store converges on a single entry.
	MemoryHygieneSuggestionSupersedeCandidate MemoryHygieneSuggestionKind = "supersede_candidate"
	// MemoryHygieneSuggestionValidityOverlapSupersede flags two accepted
	// memories that share (scope, type) and have overlapping temporal
	// validity windows (valid_from/valid_to) while carrying facts that
	// differ above the similarity threshold. The older memory is
	// superseded by the newer one on apply, and the apply path keeps
	// scope / type / refs so the retrieval surface stays consistent.
	// Unlike SupersedeCandidate this detector is gated by window
	// overlap: memories with disjoint validity windows are treated as
	// separate historical facts.
	MemoryHygieneSuggestionValidityOverlapSupersede MemoryHygieneSuggestionKind = "validity_overlap_supersede"
	// MemoryHygieneSuggestionLowQualityCandidate flags a status=candidate
	// memory whose fact text matches the deterministic low-quality
	// classifier (#857). The apply path rejects the candidate so the
	// inbox view stays focused on durable signals; accepted memories are
	// never touched. The suggestion only fires for status=candidate, and
	// extracted-hidden rows are inspected only when the caller opts in
	// via MemoryHygieneScanCriteria.IncludeHiddenCandidates so the
	// default scan stays predictable.
	MemoryHygieneSuggestionLowQualityCandidate MemoryHygieneSuggestionKind = "low_quality_candidate"
)

// MemoryHygieneScanCriteria carries the inputs the hygiene scanner
// consumes. Scopes default to every scope when empty; the staleness
// threshold controls the expiry suggestion window and falls back to a
// usecase default when zero. SimilarityThreshold tunes the
// supersede_candidate detector — 0 uses the usecase default (0.6).
//
// IncludeHiddenCandidates expands the candidate-noise pass to include
// extracted-hidden rows (low-quality auto-extractions kept for audit).
// Without it the scan only inspects visible candidates so the default
// view matches what the operator sees in the inbox.
type MemoryHygieneScanCriteria struct {
	Scopes                  []domtypes.MemoryScope
	StalenessThreshold      time.Duration
	SimilarityThreshold     float64
	Now                     time.Time
	IncludeHiddenCandidates bool
	Budget                  MemoryHygieneScanBudget
	Cursor                  string
}

// MemoryHygieneSuggestion is one scan hit. The raw Fact, SanitizedFact, and
// ReplacementFact fields are internal apply inputs and intentionally excluded
// from JSON. Their corresponding Preview fields are current-rule-sanitized,
// UTF-8-safe, bounded values suitable for CLI and MCP output.
// DuplicateMemoryID is set on duplicate hits so the reader sees both sides of
// the pair; ReplacementMemoryID is set on supersede candidates.
// Similarity is the computed word-Jaccard score (0.0-1.0) — zero on
// everything except supersede_candidate.
//
// Status / Source are populated for low_quality_candidate suggestions so
// the reviewer can confirm the row is still a candidate (and which
// extraction source produced it) before approving the apply path.
// QualityReasons enumerates the deterministic noise markers that
// classified the candidate as low-quality — the same vocabulary the
// extractor diagnostics expose (#857).
type MemoryHygieneSuggestion struct {
	MemoryID                    domtypes.MemoryID           `json:"memory_id"`
	Kind                        MemoryHygieneSuggestionKind `json:"kind"`
	Reason                      string                      `json:"reason"`
	Fact                        string                      `json:"-"`
	FactPreview                 string                      `json:"fact_preview"`
	FactPreviewTruncated        bool                        `json:"fact_preview_truncated,omitempty"`
	SanitizedFact               string                      `json:"-"`
	SanitizedFactPreview        string                      `json:"sanitized_fact_preview,omitempty"`
	SanitizedPreviewTruncated   bool                        `json:"sanitized_preview_truncated,omitempty"`
	DuplicateMemoryID           domtypes.MemoryID           `json:"duplicate_memory_id,omitempty"`
	ReplacementMemoryID         domtypes.MemoryID           `json:"replacement_memory_id,omitempty"`
	ReplacementFact             string                      `json:"-"`
	ReplacementFactPreview      string                      `json:"replacement_fact_preview,omitempty"`
	ReplacementPreviewTruncated bool                        `json:"replacement_preview_truncated,omitempty"`
	Similarity                  float64                     `json:"similarity,omitempty"`
	Scope                       domtypes.MemoryScope        `json:"-"`
	UpdatedAt                   time.Time                   `json:"updated_at"`
	Status                      domtypes.MemoryStatus       `json:"status,omitempty"`
	Source                      domtypes.MemorySource       `json:"source,omitempty"`
	QualityReasons              []string                    `json:"quality_reasons,omitempty"`
}

// MemoryHygieneScanResult summarises a single scan run. Suggestions keeps
// the list so JSON consumers get a stable shape regardless of which
// category was populated; the counts mirror what the CLI renders as a
// human-readable summary.
type MemoryHygieneScanResult struct {
	Suggestions                   []MemoryHygieneSuggestion
	RedactionHitCount             int
	ExpiryCandidateCount          int
	DuplicateCount                int
	SupersedeCandidateCount       int
	ValidityOverlapSupersedeCount int
	LowQualityCandidateCount      int
	Complete                      bool
	Partial                       bool
	StopReason                    MemoryHygieneStopReason
	Consistency                   MemoryHygieneScanConsistency
	ConsistencyReason             MemoryHygieneConsistencyReason
	NextCursor                    string
	Usage                         MemoryHygieneScanUsage
}

// MemoryHygieneApplyCriteria carries the inputs to the apply path. IDs
// reference memories the caller already saw in a Scan result; the usecase
// revalidates each requested ID and its same-scope peers before applying a
// transition.
//
// IncludeHiddenCandidates mirrors the scan flag so an apply targeting a
// previously-hidden candidate ID includes it in targeted revalidation. Without
// it, the apply fails with "no current hygiene suggestion".
type MemoryHygieneApplyCriteria struct {
	MemoryIDs               []string
	StalenessThreshold      time.Duration
	Now                     time.Time
	IncludeHiddenCandidates bool
}

// MemoryHygieneApplyResult mirrors the inbox batch output shape so both
// surfaces expose the same processed / failure breakdown.
type MemoryHygieneApplyResult struct {
	Applied  []MemoryHygieneApplied
	Failures []MemoryHygieneApplyFailure
}

// MemoryHygieneApplied describes one memory that transitioned because of
// an apply run. Kind / Transition tell the reviewer exactly what the
// usecase did so they can re-inspect the store later.
type MemoryHygieneApplied struct {
	MemoryID   string
	Kind       MemoryHygieneSuggestionKind
	Transition string
	Details    MemoryDetails
}

// MemoryHygieneApplyFailure reports per-id errors so the caller can
// retry only the tail that failed.
type MemoryHygieneApplyFailure struct {
	MemoryID string
	Error    string
}
