package usecase

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

type storeManagementUsecase struct {
	storeManager application.StoreManager
}

// NewStoreManagementUsecase creates a StoreManagementUsecase.
func NewStoreManagementUsecase(storeManager application.StoreManager) StoreManagementUsecase {
	return &storeManagementUsecase{storeManager: storeManager}
}

func (u *storeManagementUsecase) Initialize(ctx context.Context) error {
	if err := u.storeManager.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	return nil
}

func (u *storeManagementUsecase) CreateBackup(ctx context.Context, outputPath string, overwrite bool) error {
	if strings.TrimSpace(outputPath) == "" {
		return xerrors.Errorf("output path must not be empty")
	}
	if err := u.storeManager.CreateBackup(ctx, strings.TrimSpace(outputPath), overwrite); err != nil {
		return xerrors.Errorf("failed to create store backup: %w", err)
	}
	return nil
}

func (u *storeManagementUsecase) RestoreBackup(ctx context.Context, inputPath string, overwrite bool) error {
	if strings.TrimSpace(inputPath) == "" {
		return xerrors.Errorf("input path must not be empty")
	}
	if err := u.storeManager.RestoreBackup(ctx, strings.TrimSpace(inputPath), overwrite); err != nil {
		return xerrors.Errorf("failed to restore store backup: %w", err)
	}
	return nil
}

func (u *storeManagementUsecase) CollectGarbage(
	ctx context.Context,
	before time.Time,
	target apptypes.GarbageCollectionTarget,
	dryRun bool,
) (apptypes.CollectGarbageResult, error) {
	if before.IsZero() {
		return apptypes.CollectGarbageResult{}, xerrors.Errorf("before timestamp is required")
	}

	if _, ok := apptypes.GarbageCollectionTargetFrom(target.String()); !ok {
		return apptypes.CollectGarbageResult{}, xerrors.Errorf("unsupported garbage-collection target: %s", target)
	}

	deletedCount, err := u.storeManager.CollectGarbage(ctx, before, target, dryRun)
	if err != nil {
		return apptypes.CollectGarbageResult{}, xerrors.Errorf("failed to collect garbage: %w", err)
	}

	return apptypes.CollectGarbageResultOf(deletedCount, before, dryRun), nil
}

func (u *storeManagementUsecase) CloseStaleSessions(
	ctx context.Context,
	staleAfter time.Duration,
	dryRun bool,
) (apptypes.CloseStaleSessionsResult, error) {
	closedCount, err := u.storeManager.CloseStaleSessions(ctx, staleAfter, dryRun)
	if err != nil {
		return apptypes.CloseStaleSessionsResult{}, xerrors.Errorf("failed to close stale sessions: %w", err)
	}

	return apptypes.CloseStaleSessionsResultOf(closedCount), nil
}
