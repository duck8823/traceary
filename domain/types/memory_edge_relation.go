package types

import "strings"

// MemoryEdgeRelation is the typed relationship carried by a
// MemoryEdge. The v1 vocabulary is intentionally small (see
// docs/architecture/temporal-memory.md): "supersedes", "contradicts",
// "supports", "related-to", "causes". Unknown values are preserved
// round-trip, but default views filter them out so forward-compat
// edges written by newer Traceary versions do not trip legacy
// consumers.
type MemoryEdgeRelation string

const (
	// MemoryEdgeRelationSupersedes records that `from` replaces `to`.
	// Complementary to the existing `supersedes_memory_id` column:
	// the column captures the chain, the edge can carry extra
	// annotations (e.g. a future `reason` field).
	MemoryEdgeRelationSupersedes MemoryEdgeRelation = "supersedes"
	// MemoryEdgeRelationContradicts marks `from` as directly
	// contradicting `to`. Feeds into future hygiene detectors.
	MemoryEdgeRelationContradicts MemoryEdgeRelation = "contradicts"
	// MemoryEdgeRelationSupports marks `from` as evidence for `to`
	// so "why do we believe X" walks become possible.
	MemoryEdgeRelationSupports MemoryEdgeRelation = "supports"
	// MemoryEdgeRelationRelatedTo is the weak catch-all link.
	// Callers should prefer a more specific relation when one fits.
	MemoryEdgeRelationRelatedTo MemoryEdgeRelation = "related-to"
	// MemoryEdgeRelationCauses marks `from` as causing `to`. Scoped
	// to causal / dependency links.
	MemoryEdgeRelationCauses MemoryEdgeRelation = "causes"
)

// MemoryEdgeRelationOf validates and normalises a raw relation
// string. Leading / trailing whitespace is stripped; the resulting
// value must be non-empty. Unknown values are accepted (forward
// compat) — callers that want strict vocabulary can check against
// the KnownMemoryEdgeRelations set.
func MemoryEdgeRelationOf(value string) MemoryEdgeRelation {
	return MemoryEdgeRelation(strings.TrimSpace(value))
}

// String returns the string representation.
func (r MemoryEdgeRelation) String() string { return string(r) }

// KnownMemoryEdgeRelations lists the v1 vocabulary for diagnostics
// and help text. Do not use for validation — unknown relations are
// intentionally allowed so newer Traceary versions can write without
// breaking older readers.
var KnownMemoryEdgeRelations = []MemoryEdgeRelation{
	MemoryEdgeRelationSupersedes,
	MemoryEdgeRelationContradicts,
	MemoryEdgeRelationSupports,
	MemoryEdgeRelationRelatedTo,
	MemoryEdgeRelationCauses,
}
