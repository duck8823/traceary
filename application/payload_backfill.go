package application

import (
	"context"

	"github.com/duck8823/traceary/application/types"
)

// PayloadBackfill is the live-store in-place body rewrite port.
// Unlike payload rehearsal it mutates events.body through the codec and
// never freezes writers or writes a shadow table.
type PayloadBackfill interface {
	// Preview reports eligible-row counts without mutating the store.
	Preview(context.Context, types.PayloadBackfillConfig) (types.PayloadBackfillResult, error)
	// Run starts a new backfill (or continues only when no resumable run exists).
	Run(context.Context, types.PayloadBackfillConfig) (types.PayloadBackfillResult, error)
	// Resume continues a running/paused run under the same recipe version.
	Resume(context.Context, types.PayloadBackfillConfig) (types.PayloadBackfillResult, error)
	// Status reports the latest run without advancing it.
	Status(context.Context) (types.PayloadBackfillResult, error)
}
