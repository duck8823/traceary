package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// OneShotRepairStore exposes distinct no-write preview and atomic persistence paths.
type OneShotRepairStore interface {
	PreviewOneShotSessions(ctx context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error)
	ApplyOneShotSessions(ctx context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error)
}

// OneShotRepairSafetyStore provides the mandatory rollback snapshot and store preparation.
type OneShotRepairSafetyStore interface {
	CreateBackup(ctx context.Context, outputPath string, overwrite bool) error
	Initialize(ctx context.Context) error
}
