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

type SearchProjectionAbandonStore interface {
	AbandonSearchProjection(context.Context, time.Time) (apptypes.SearchProjectionAbandonResult, error)
}

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
	wallCtx, cancel := context.WithTimeout(ctx, b.WallTime)
	defer cancel()
	status, err := u.store.SearchProjectionStatus(wallCtx)
	if err != nil {
		return apptypes.SearchProjectionProgress{}, xerrors.Errorf("inspect projection before resume: %w", err)
	}
	if status.State != "rebuilding" && (status.State != "drifted" || status.Phase != "cleanup") {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "no generation is rebuilding"}
	}
	if status.ConfigHash != b.ConfigHash() {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "budget does not match generation configuration"}
	}
	snapshot, err := u.store.SelectSnapshot(wallCtx, b, now.UTC())
	if err != nil {
		var oversized *apptypes.SearchProjectionOversizeError
		if errors.As(err, &oversized) && snapshot.Generation.GenerationID != "" {
			if markErr := u.markFailed(ctx, b.LockTime, snapshot.Generation, oversized.Class, now.UTC()); markErr != nil {
				return apptypes.SearchProjectionProgress{}, markErr
			}
		}
		return apptypes.SearchProjectionProgress{}, err
	}
	plan, err := PlanProjectionBatch(snapshot, b)
	if err != nil {
		var oversized *apptypes.SearchProjectionOversizeError
		if errors.As(err, &oversized) {
			if markErr := u.markFailed(ctx, b.LockTime, snapshot.Generation, oversized.Class, now.UTC()); markErr != nil {
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

func (u *SearchProjectionUsecase) ResumeUntil(ctx context.Context, b apptypes.SearchProjectionBudget, opts apptypes.SearchProjectionRunOptions, now time.Time) (apptypes.SearchProjectionRunResult, error) {
	started := time.Now()
	result := apptypes.SearchProjectionRunResult{}
	if opts.MaxBatches <= 0 || opts.TotalWallTime <= 0 {
		return result, &apptypes.SearchProjectionNoProgressError{Reason: "multi-batch bounds must be positive"}
	}
	runCtx, cancel := context.WithTimeout(ctx, opts.TotalWallTime)
	defer cancel()
	for result.Batches < opts.MaxBatches {
		if err := runCtx.Err(); err != nil {
			result.StopReason = "total_wall_time"
			result.ElapsedMilliseconds = time.Since(started).Milliseconds()
			return result, nil
		}
		progress, err := u.Resume(runCtx, b, now.UTC())
		if err != nil {
			return result, err
		}
		result.Batches++
		result.Progress.Selected += progress.Selected
		result.Progress.Written += progress.Written
		result.Progress.Evicted += progress.Evicted
		result.Progress.Cleaned += progress.Cleaned
		result.Progress.StoredBytes += progress.StoredBytes
		result.Progress.DecodedBytes += progress.DecodedBytes
		result.Progress.WrittenBytes += progress.WrittenBytes
		result.Progress.CleanupBytes += progress.CleanupBytes
		result.Progress.Completed = progress.Completed
		result.Progress.GenerationID = progress.GenerationID
		if progress.Completed {
			result.StopReason = "complete"
			break
		}
	}
	if result.StopReason == "" {
		result.StopReason = "max_batches"
	}
	result.ElapsedMilliseconds = time.Since(started).Milliseconds()
	return result, nil
}

func (u *SearchProjectionUsecase) Abandon(ctx context.Context, now time.Time) (apptypes.SearchProjectionAbandonResult, error) {
	store, ok := u.store.(SearchProjectionAbandonStore)
	if !ok {
		return apptypes.SearchProjectionAbandonResult{}, &apptypes.SearchProjectionNoProgressError{Reason: "projection store does not support abandon"}
	}
	result, err := store.AbandonSearchProjection(ctx, now.UTC())
	if err != nil {
		return result, xerrors.Errorf("abandon search projection: %w", err)
	}
	return result, nil
}

// markFailed is an explicit recovery transition. It uses the original parent
// and its own lock deadline so an exhausted batch wall budget cannot strand the
// generation in rebuilding.
//
//nolint:wrapcheck // Preserve typed recovery-transition errors.
func (u *SearchProjectionUsecase) markFailed(ctx context.Context, lock time.Duration, generation apptypes.SearchProjectionGeneration, class string, now time.Time) error {
	failureCtx, cancel := context.WithTimeout(ctx, lock)
	defer cancel()
	return u.store.MarkFailed(failureCtx, generation.GenerationID, generation.SourceRevision, class, now)
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) Inspect(ctx context.Context) (apptypes.SearchProjectionStatus, error) {
	return u.store.SearchProjectionStatus(ctx)
}
