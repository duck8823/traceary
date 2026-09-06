package types

import "fmt"

// LegacyHookNonEmptyError stops an upgrade that would drop a non-NULL
// event_metadata_projection.legacy_source_hook value. The live store is
// left untouched; the candidate is discarded. This is the only binary
// message that carries the 0.48.2 retrieval procedure for those rows.
type LegacyHookNonEmptyError struct {
	RowCount int
}

func (e *LegacyHookNonEmptyError) Error() string {
	count := 0
	if e != nil {
		count = e.RowCount
	}
	return fmt.Sprintf(
		"event_metadata_projection.legacy_source_hook holds %d non-null row(s); refusing to drop them. Pin Traceary 0.48.2 and retrieve the rows before this upgrade: SELECT id, kind, source_hook, legacy_source_hook FROM event_metadata_projection WHERE legacy_source_hook IS NOT NULL. Retry the upgrade once every remaining value is NULL. DROP COLUMN moves pages to the freelist; it does not shrink the file without VACUUM or a candidate rewrite.",
		count,
	)
}
