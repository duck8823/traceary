package usecase

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// StoreManagementUsecase consolidates store lifecycle operations.
type StoreManagementUsecase interface {
	// Initialize creates the store and applies migrations.
	Initialize(ctx context.Context) error

	// CreateBackup creates a backup of the store.
	CreateBackup(ctx context.Context, outputPath string, overwrite bool) error

	// RestoreBackup restores a backup into the store.
	RestoreBackup(ctx context.Context, inputPath string, overwrite bool) error

	// CollectGarbage removes events older than the given time.
	CollectGarbage(ctx context.Context, before time.Time, target apptypes.GarbageCollectionTarget, dryRun bool) (apptypes.CollectGarbageResult, error)

	// CloseStaleSessions closes sessions active beyond the given duration.
	CloseStaleSessions(ctx context.Context, staleAfter time.Duration, dryRun bool) (apptypes.CloseStaleSessionsResult, error)
}
