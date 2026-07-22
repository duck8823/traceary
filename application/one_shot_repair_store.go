package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// OneShotRepairStore exposes distinct no-write preview and atomic apply paths.
type OneShotRepairStore interface {
	PreviewOneShotSessions(ctx context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error)
	ApplyOneShotSessions(ctx context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error)
}
