package application

import (
	"context"
	"time"
)

// StoreManager provides store lifecycle and maintenance operations.
type StoreManager interface {
	// Initialize creates the store and applies migrations.
	Initialize(ctx context.Context) error
	// CreateBackup creates a backup of the store.
	CreateBackup(ctx context.Context, outputPath string, overwrite bool) error
	// RestoreBackup restores a backup.
	RestoreBackup(ctx context.Context, inputPath string, overwrite bool) error
	// CollectGarbage removes events older than the given time. Returns the count of deleted events.
	CollectGarbage(ctx context.Context, before time.Time, dryRun bool) (int, error)
	// CloseStaleSessions closes sessions that have been active beyond a threshold. Returns the count of closed sessions.
	CloseStaleSessions(ctx context.Context, staleAfter time.Duration, dryRun bool) (int, error)
}
