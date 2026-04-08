package usecase

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

// GarbageCollector provides old-event deletion.
type GarbageCollector interface {
	// CollectGarbage deletes events older than the given time.
	CollectGarbage(ctx context.Context, dbPath string, before time.Time, dryRun bool) (int, error)
}

// CollectGarbageInput is the input for garbage collection.
type CollectGarbageInput struct {
	DBPath string
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
	garbageCollector GarbageCollector
}

// NewCollectGarbageUsecase creates a CollectGarbageUsecase.
func NewCollectGarbageUsecase(garbageCollector GarbageCollector) CollectGarbageUsecase {
	return &collectGarbageUsecase{garbageCollector: garbageCollector}
}

// Run executes garbage collection or a dry run.
func (u *collectGarbageUsecase) Run(
	ctx context.Context,
	input CollectGarbageInput,
) (*CollectGarbageResult, error) {
	if u.garbageCollector == nil {
		return nil, xerrors.Errorf("garbage collector is not configured")
	}
	if strings.TrimSpace(input.DBPath) == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}
	if input.Before.IsZero() {
		return nil, xerrors.Errorf("before timestamp is required")
	}

	deletedCount, err := u.garbageCollector.CollectGarbage(
		ctx,
		strings.TrimSpace(input.DBPath),
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
