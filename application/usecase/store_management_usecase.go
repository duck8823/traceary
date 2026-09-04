package usecase

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// StoreManagementUsecase consolidates store lifecycle operations.
type StoreManagementUsecase interface {
	// Initialize creates the store and applies implicit (non-offline) migrations.
	Initialize(ctx context.Context) error
	// PreviewOfflineMigrations lists pending data-dependent versions without applying them.
	PreviewOfflineMigrations(ctx context.Context) ([]int64, error)
	// InspectOneOffRepairRetirement reports outstanding/never-ran/retired
	// states for completed one-off repairs.
	InspectOneOffRepairRetirement(ctx context.Context) (apptypes.OneOffRepairRetirement, error)
	// InspectBoundDrop reports the preflight count and digest for a pending
	// retired-table drop. Zero rows means no approval is required.
	InspectBoundDrop(ctx context.Context) (apptypes.BoundDropInspection, error)

	// CreateBackup creates a backup of the store.
	CreateBackup(ctx context.Context, outputPath string, overwrite bool) error

	// RestoreBackup restores a backup into the store.
	RestoreBackup(ctx context.Context, inputPath string, overwrite bool) error

	// CollectGarbage removes events older than the given time.
	CollectGarbage(ctx context.Context, before time.Time, target apptypes.GarbageCollectionTarget, dryRun bool) (apptypes.CollectGarbageResult, error)

	// CloseStaleSessions closes sessions that started before the threshold and
	// have no activity inside it, excluding the protected active sessions.
	CloseStaleSessions(ctx context.Context, staleAfter time.Duration, dryRun bool, protectedSessionIDs []types.SessionID) (apptypes.CloseStaleSessionsResult, error)

	// CreateStoreArchive exports GC-eligible rows to a versioned archive package.
	// When DeleteAfterVerify is set, verifies the package then deletes exact IDs.
	CreateStoreArchive(ctx context.Context, params apptypes.StoreArchiveCreateParams) (apptypes.StoreArchiveResult, error)
	// VerifyStoreArchive checks package integrity (and decryptability when sealed).
	VerifyStoreArchive(ctx context.Context, path string, passphrase []byte) error
	// RestoreStoreArchive imports archived rows idempotently by primary key.
	RestoreStoreArchive(ctx context.Context, path string, passphrase []byte, dryRun bool) (apptypes.StoreArchiveRestoreResult, error)
}
