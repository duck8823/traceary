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
)

// MemoryHygieneScanCriteria carries the inputs the hygiene scanner
// consumes. Scopes default to every scope when empty; the staleness
// threshold controls the expiry suggestion window and falls back to a
// usecase default when zero.
type MemoryHygieneScanCriteria struct {
	Scopes             []domtypes.MemoryScope
	StalenessThreshold time.Duration
	Now                time.Time
}

// MemoryHygieneSuggestion is the serializable view of a single scan hit.
// SanitizedFact is populated for redaction hits so the apply path can
// propose a supersede with the masked content; DuplicateMemoryID is set
// on duplicate hits so the reader sees both sides of the pair.
type MemoryHygieneSuggestion struct {
	MemoryID          domtypes.MemoryID
	Kind              MemoryHygieneSuggestionKind
	Reason            string
	Fact              string
	SanitizedFact     string
	DuplicateMemoryID domtypes.MemoryID
	Scope             domtypes.MemoryScope
	UpdatedAt         time.Time
}

// MemoryHygieneScanResult summarises a single scan run. Suggestions keeps
// the list so JSON consumers get a stable shape regardless of which
// category was populated; the counts mirror what the CLI renders as a
// human-readable summary.
type MemoryHygieneScanResult struct {
	Suggestions          []MemoryHygieneSuggestion
	RedactionHitCount    int
	ExpiryCandidateCount int
	DuplicateCount       int
}
