package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

// SegmentMigrationRepository owns the segment-specific durable protocol. Each
// call advances at most one bounded journal boundary.
type SegmentMigrationRepository interface {
	StartSegmentMigration(context.Context, apptypes.SegmentMigrationStart) (domain.SegmentMigrationRun, error)
	LoadSegmentMigration(context.Context, string) (domain.SegmentMigrationRun, error)
	ExecuteSegmentMigrationAction(context.Context, string, domain.SegmentMigrationAction, apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error)
	ExecuteSegmentMigrationRollbackAction(context.Context, string, domain.SegmentMigrationRollbackAction, apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error)
	AdvanceSegmentMigration(context.Context, string, apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error)
	AdvanceSegmentMigrationRollback(context.Context, string, apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error)
	RecoverSegmentMigration(context.Context, string, apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error)
}

// SegmentMigrationUsecase is the application orchestration contract.
type SegmentMigrationUsecase interface {
	Start(context.Context, apptypes.SegmentMigrationStart) (domain.SegmentMigrationRun, error)
	Resume(context.Context, string, apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error)
	Rollback(context.Context, string, apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error)
	Recover(context.Context, string, apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error)
}
