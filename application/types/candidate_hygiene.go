package types

// CandidateHygieneCounts summarises the hygiene composition of durable-memory
// candidates within a bounded scan window. The four flag counts (Stale,
// Duplicate, FragmentLike, ExtractedHidden) are independent diagnostic
// dimensions and may overlap — a single candidate can be both stale and
// fragment-like, so they do not partition the candidate total.
// LikelyActionable is the complement: candidates flagged by none of the four,
// i.e. the queue an operator should actually review.
//
// When the producing scan saturates its limit (signalled separately by the
// snapshot's scan_limit_reached), these counts reflect the scanned sample
// rather than the full backlog, mirroring the accepted/candidate totals.
type CandidateHygieneCounts struct {
	// Stale counts candidates last updated before the staleness threshold.
	Stale int
	// Duplicate counts candidates that share an exact identity (same scope,
	// memory type, and fact) with another candidate in the scanned window,
	// matching the extraction dedupe key. Similarity / near-duplicate
	// detection stays in `memory admin hygiene scan`.
	Duplicate int
	// FragmentLike counts candidates whose fact looks like a diff fragment or
	// generated code (the obvious code/diff fragments that should not be
	// durable memories).
	FragmentLike int
	// ExtractedHidden counts candidates routed to the extracted-hidden source
	// by the extraction quality gate.
	ExtractedHidden int
	// LikelyActionable counts candidates flagged by none of the above.
	LikelyActionable int
}
