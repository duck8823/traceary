package types

import "fmt"

// PayloadDecodeRefusedError stops an upgrade whose candidate holds a lane
// row the migration-only decoder cannot verify. The live store is left
// untouched; the candidate is discarded.
type PayloadDecodeRefusedError struct {
	Lane   string // "body" | "command" | "input" | "output"
	RowID  string // events.id for body; command_audits.event_id otherwise
	Reason string // decoder reason, e.g. "incomplete metadata", "stored length mismatch", "payload decode failed"
}

func (e *PayloadDecodeRefusedError) Error() string {
	if e == nil {
		return "payload decode refused (lane= row=): "
	}
	return fmt.Sprintf("payload decode refused (lane=%s row=%s): %s", e.Lane, e.RowID, e.Reason)
}
