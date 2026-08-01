package application

import (
	"context"

	"github.com/duck8823/traceary/application/types"
)

// PayloadRehearsalPreview is a strictly read-only copied-store inspection port.
type PayloadRehearsalPreview interface {
	Preview(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
}

// PayloadRehearsalRunner persists bounded shadow rows and resumable checkpoints.
type PayloadRehearsalRunner interface {
	Rollback(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
}

// PayloadRehearsalScrubHandle keeps a scrub lease and SQLite state opaque.
type PayloadRehearsalScrubHandle interface{ PayloadRehearsalScrubHandle() }

// PayloadRehearsalScrubWorkflow exposes one bounded verification page at a time.
type PayloadRehearsalScrubWorkflow interface {
	PrepareScrub(context.Context, types.PayloadRehearsalConfig) (PayloadRehearsalScrubHandle, types.PayloadRehearsalMetrics, error)
	AdvanceScrubField(context.Context, PayloadRehearsalScrubHandle, types.PayloadRehearsalField) (types.PayloadRehearsalMetrics, bool, error)
	CompleteScrub(context.Context, PayloadRehearsalScrubHandle) (types.PayloadRehearsalMetrics, error)
	ReleaseScrub(context.Context, PayloadRehearsalScrubHandle) error
	CloseScrub(PayloadRehearsalScrubHandle) error
}

// PayloadRehearsalRunHandle keeps connection, identity, lease, codec and WAL
// state opaque to application orchestration.
type PayloadRehearsalRunHandle interface{ PayloadRehearsalRunHandle() }

// PayloadRehearsalRunWorkflow exposes one bounded field advance at a time.
// Implementations own persistence primitives; callers own ordering, deadline,
// pause and completion policy.
type PayloadRehearsalRunWorkflow interface {
	Prepare(context.Context, types.PayloadRehearsalConfig, types.PayloadRehearsalRunCommand) (PayloadRehearsalRunHandle, types.PayloadRehearsalMetrics, error)
	AdvanceField(context.Context, PayloadRehearsalRunHandle, types.PayloadRehearsalField) (types.PayloadRehearsalMetrics, bool, error)
	Pause(context.Context, PayloadRehearsalRunHandle) (types.PayloadRehearsalMetrics, error)
	Complete(context.Context, PayloadRehearsalRunHandle) (types.PayloadRehearsalMetrics, error)
	Close(PayloadRehearsalRunHandle) error
}
