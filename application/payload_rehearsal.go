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
	Run(context.Context, types.PayloadRehearsalConfig, types.PayloadRehearsalRunCommand) (types.PayloadRehearsalMetrics, error)
	Scrub(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Rollback(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
}
