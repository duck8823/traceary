package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// CollectGarbageInput is the input for garbage collection.
type CollectGarbageInput struct {
	Before time.Time
	DryRun bool
}

// CollectGarbageResult is the result of a garbage-collection run.
type CollectGarbageResult struct {
	DeletedCount int
	Before       time.Time
	DryRun       bool
}

// CollectGarbageUsecase deletes events based on retention.
type CollectGarbageUsecase interface {
	// Run executes garbage collection or a dry run.
	Run(ctx context.Context, input CollectGarbageInput) (*CollectGarbageResult, error)
}

type collectGarbageUsecase struct {
	storeManager application.StoreManager
}

// NewCollectGarbageUsecase creates a CollectGarbageUsecase.
func NewCollectGarbageUsecase(storeManager application.StoreManager) CollectGarbageUsecase {
	return &collectGarbageUsecase{storeManager: storeManager}
}

// Run executes garbage collection or a dry run.
func (u *collectGarbageUsecase) Run(
	ctx context.Context,
	input CollectGarbageInput,
) (*CollectGarbageResult, error) {
	if u.storeManager == nil {
		return nil, xerrors.Errorf("store manager is not configured")
	}
	if input.Before.IsZero() {
		return nil, xerrors.Errorf("before timestamp is required")
	}

	deletedCount, err := u.storeManager.CollectGarbage(
		ctx,
		input.Before,
		input.DryRun,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to collect garbage: %w", err)
	}

	return &CollectGarbageResult{
		DeletedCount: deletedCount,
		Before:       input.Before,
		DryRun:       input.DryRun,
	}, nil
}
