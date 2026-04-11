package usecase

import (
	"context"
	"time"
)

// StoreMaintenanceUsecase consolidates store lifecycle operations.
type StoreMaintenanceUsecase interface {
	// Initialize creates the store and applies migrations.
	Initialize(ctx context.Context) error

	// CreateBackup creates a backup of the store.
	CreateBackup(ctx context.Context, outputPath string, overwrite bool) error

	// RestoreBackup restores a backup into the store.
	RestoreBackup(ctx context.Context, inputPath string, overwrite bool) error

	// CollectGarbage removes events older than the given time.
	CollectGarbage(ctx context.Context, before time.Time, dryRun bool) (*CollectGarbageResult, error)

	// CloseStaleSession closes sessions active beyond the given duration.
	CloseStaleSession(ctx context.Context, staleAfter time.Duration, dryRun bool) (*CloseStaleSessionsResult, error)
}
