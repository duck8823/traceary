package types

// MemoryImportResult summarises a single run of the Codex memory import
// usecase. Counts are reported per category so the CLI can render a
// human-friendly summary and `--json` consumers can track drift across
// runs without replaying the full candidate list.
type MemoryImportResult struct {
	// Imported is the set of newly persisted candidate memories.
	Imported []MemoryDetails
	// SkippedDuplicateCount is the number of candidates that were already
	// represented in the durable-memory store (same scope + source path +
	// sanitized fact) and were therefore intentionally not re-created.
	SkippedDuplicateCount int
	// SkippedRejectedCount is the number of candidates that matched an
	// existing memory whose current status is rejected/superseded/expired.
	// The import path refuses to resurrect them, so they are counted
	// separately to help the operator understand why no new row appeared.
	SkippedRejectedCount int
	// Warnings carries non-fatal parser or sanitizer messages that should
	// surface in the CLI output but do not stop the run.
	Warnings []string
}
