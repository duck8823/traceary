package types

import "fmt"

// DedupeArchiveRestoreRefusedError stops an upgrade that cannot restore
// event_content_dedupe_archive rows into events. The live store is left
// untouched; the candidate is discarded.
type DedupeArchiveRestoreRefusedError struct {
	RowCount int
	Reason   string
	EventID  string
}

func (e *DedupeArchiveRestoreRefusedError) Error() string {
	if e == nil {
		return "dedupe archive holds 0 rows: restore refused"
	}
	if e.EventID != "" {
		return fmt.Sprintf("dedupe archive holds %d rows: restore refused: %s (%s)", e.RowCount, e.Reason, e.EventID)
	}
	return fmt.Sprintf("dedupe archive holds %d rows: restore refused: %s", e.RowCount, e.Reason)
}
