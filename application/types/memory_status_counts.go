package types

// MemoryStatusCounts holds the true per-status durable-memory row counts,
// independent of any list scan limit. It backs the reliability pane's
// candidate/accepted totals when the bounded summary scan is saturated
// (see topReliabilityMemoryScanLimit).
type MemoryStatusCounts struct {
	Accepted  int
	Candidate int
}
