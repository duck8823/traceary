//nolint:wrapcheck // Typed repository errors are the public use-case contract.
package usecase

import (
	"context"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

type segmentMigrationUsecase struct {
	repository application.SegmentMigrationRepository
}

// NewSegmentMigrationUsecase composes the segment-specific resumable protocol.
func NewSegmentMigrationUsecase(repository application.SegmentMigrationRepository) application.SegmentMigrationUsecase {
	return &segmentMigrationUsecase{repository: repository}
}

func (u *segmentMigrationUsecase) Start(ctx context.Context, command apptypes.SegmentMigrationStart) (domain.SegmentMigrationRun, error) {
	return u.repository.StartSegmentMigration(ctx, command)
}

func (u *segmentMigrationUsecase) Resume(ctx context.Context, id string, budget apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if !budget.Valid() {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	var run domain.SegmentMigrationRun
	var err error
	for range budget.MaxSteps {
		run, err = u.repository.LoadSegmentMigration(opCtx, id)
		if err != nil || run.Phase == domain.SegmentMigrationVerifiedShadow {
			return run, err
		}
		action, ok := run.NextAction()
		if !ok {
			return run, domain.ErrSegmentMigrationTransition
		}
		run, err = u.repository.ExecuteSegmentMigrationAction(opCtx, id, action, budget)
		if err != nil || run.Phase == domain.SegmentMigrationVerifiedShadow {
			return run, err
		}
	}
	return run, apptypes.ErrSegmentMigrationIncomplete
}

func (u *segmentMigrationUsecase) Rollback(ctx context.Context, id string, budget apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if !budget.Valid() {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	var run domain.SegmentMigrationRun
	var err error
	for range budget.MaxSteps {
		run, err = u.repository.AdvanceSegmentMigrationRollback(opCtx, id, budget)
		if err != nil || run.Phase == domain.SegmentMigrationRolledBack {
			return run, err
		}
	}
	return run, apptypes.ErrSegmentMigrationIncomplete
}

func (u *segmentMigrationUsecase) Recover(ctx context.Context, id string, budget apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if !budget.Valid() {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	return u.repository.RecoverSegmentMigration(opCtx, id, budget)
}

var _ application.SegmentMigrationUsecase = (*segmentMigrationUsecase)(nil)
