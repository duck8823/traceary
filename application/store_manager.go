package application

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
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
	CollectGarbage(ctx context.Context, before time.Time, target apptypes.GarbageCollectionTarget, dryRun bool) (int, error)
	// CloseStaleSessions closes sessions that started before the threshold and
	// have no activity inside it, excluding the protected active sessions.
	CloseStaleSessions(ctx context.Context, staleAfter time.Duration, dryRun bool, protectedSessionIDs []types.SessionID) (int, error)
}
