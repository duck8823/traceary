//nolint:revive // Public projection API names are intentionally explicit.
package usecase

import (
	"context"
	"errors"
	"golang.org/x/xerrors"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// SearchProjectionStore is the narrow persistence boundary for the explicit
// projection state machine. Policy and generation lifecycle stay here.
type SearchProjectionStore interface {
	Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error)
	SelectSnapshot(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.ProjectionSnapshot, error)
	ApplyBatch(context.Context, apptypes.ProjectionBatchPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error)
	CleanupBatch(context.Context, apptypes.ProjectionBatchPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error)
	MarkFailed(context.Context, string, int64, string, time.Time) error
	SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error)
}

type SearchProjectionUsecase struct{ store SearchProjectionStore }

// NewSearchProjectionUsecase constructs the projection workflow.
func NewSearchProjectionUsecase(store SearchProjectionStore) *SearchProjectionUsecase {
	return &SearchProjectionUsecase{store: store}
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) StartGeneration(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionGeneration, error) {
	if !b.Valid() {
		return apptypes.SearchProjectionGeneration{}, &apptypes.SearchProjectionNoProgressError{Reason: "invalid generation budget"}
	}
	status, err := u.store.SearchProjectionStatus(ctx)
	if err != nil {
		return apptypes.SearchProjectionGeneration{}, xerrors.Errorf("inspect projection before start: %w", err)
	}
	if status.State == "rebuilding" {
		return apptypes.SearchProjectionGeneration{}, &apptypes.SearchProjectionNoProgressError{Reason: "a generation is already rebuilding"}
	}
	return u.store.Start(ctx, b, now.UTC())
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) Resume(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	status, err := u.store.SearchProjectionStatus(ctx)
	if err != nil {
		return apptypes.SearchProjectionProgress{}, xerrors.Errorf("inspect projection before resume: %w", err)
	}
	if status.State != "rebuilding" && (status.State != "drifted" || status.Phase != "cleanup") {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "no generation is rebuilding"}
	}
	if status.ConfigHash != b.ConfigHash() {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "budget does not match generation configuration"}
	}
	wallCtx, cancel := context.WithTimeout(ctx, b.WallTime)
	defer cancel()
	snapshot, err := u.store.SelectSnapshot(wallCtx, b, now.UTC())
	if err != nil {
		var oversized *apptypes.SearchProjectionOversizeError
		if errors.As(err, &oversized) && snapshot.Generation.GenerationID != "" {
			if markErr := u.store.MarkFailed(wallCtx, snapshot.Generation.GenerationID, snapshot.Generation.SourceRevision, oversized.Class, now.UTC()); markErr != nil {
				return apptypes.SearchProjectionProgress{}, markErr
			}
		}
		return apptypes.SearchProjectionProgress{}, err
	}
	plan, err := PlanProjectionBatch(snapshot, b)
	if err != nil {
		var oversized *apptypes.SearchProjectionOversizeError
		if errors.As(err, &oversized) {
			if markErr := u.store.MarkFailed(wallCtx, snapshot.Generation.GenerationID, snapshot.Generation.SourceRevision, oversized.Class, now.UTC()); markErr != nil {
				return apptypes.SearchProjectionProgress{}, markErr
			}
		}
		return apptypes.SearchProjectionProgress{}, err
	}
	if err = wallCtx.Err(); err != nil {
		return apptypes.SearchProjectionProgress{}, err
	}
	if snapshot.Phase == "source" {
		return u.store.ApplyBatch(wallCtx, plan, b.LockTime, now.UTC())
	}
	return u.store.CleanupBatch(wallCtx, plan, b.LockTime, now.UTC())
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) Inspect(ctx context.Context) (apptypes.SearchProjectionStatus, error) {
	return u.store.SearchProjectionStatus(ctx)
}
