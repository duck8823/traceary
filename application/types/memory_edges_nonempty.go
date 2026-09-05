package types

import "fmt"

// MemoryEdgesNonEmptyError stops an upgrade that would drop a non-empty
// memory_edges table. The live store is left untouched; the candidate is
// discarded. This is the only binary message that carries the 0.48.2
// restore-and-export procedure for memory graph rows.
type MemoryEdgesNonEmptyError struct {
	RowCount int
}

func (e *MemoryEdgesNonEmptyError) Error() string {
	count := 0
	if e != nil {
		count = e.RowCount
	}
	return fmt.Sprintf(
		"memory_edges holds %d row(s); refusing to drop them. Pin Traceary 0.48.2 and retrieve the rows before this upgrade: export the live store with `traceary bundle export` (0.48.2 writes memory_edges.ndjson). Retry the upgrade once memory_edges is empty. DROP moves pages to the freelist; it does not shrink the file without VACUUM or a candidate rewrite.",
		count,
	)
}
