package application

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// FileRetentionInventory provides complete read-only root evidence.
type FileRetentionInventory interface {
	InspectFileRetention(ctx context.Context, request apptypes.FileRetentionInventoryRequest) (apptypes.FileRetentionInventorySnapshot, error)
}

// FileRetentionExecutor applies an exact decoded plan through a crash-retryable adapter.
type FileRetentionExecutor interface {
	ApplyFileRetention(ctx context.Context, plan apptypes.FileRetentionPlan, confirmedPlanID string, now time.Time) (apptypes.FileRetentionApplyResult, error)
}
