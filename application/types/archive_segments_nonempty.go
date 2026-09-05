package types

import "fmt"

// ArchiveSegmentsNonEmptyError stops an upgrade that would drop a non-empty
// archive_segments table. The live store is left untouched; the candidate is
// discarded. This is the only binary message that carries the 0.48.2
// restore-and-export procedure.
type ArchiveSegmentsNonEmptyError struct {
	RowCount int
}

func (e *ArchiveSegmentsNonEmptyError) Error() string {
	count := 0
	if e != nil {
		count = e.RowCount
	}
	return fmt.Sprintf(
		"archive_segments holds %d row(s); refusing to drop them. Pin Traceary 0.48.2 and retrieve the rows before this upgrade: (1) restore each archive package with `traceary store compact --archive-restore <package>`; (2) export the live store with `traceary bundle export`. Retry the upgrade once archive_segments is empty. DROP moves pages to the freelist; it does not shrink the file without VACUUM or a candidate rewrite.",
		count,
	)
}
