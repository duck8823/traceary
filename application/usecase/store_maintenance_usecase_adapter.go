package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"
)

type storeMaintenanceUsecaseAdapter struct {
	initializeStore    InitializeStoreUsecase
	createBackup       CreateStoreBackupUsecase
	restoreBackup      RestoreStoreBackupUsecase
	collectGarbage     CollectGarbageUsecase
	closeStaleSessions CloseStaleSessionsUsecase
}

// NewStoreMaintenanceUsecaseAdapter creates a StoreMaintenanceUsecase that delegates to existing usecases.
func NewStoreMaintenanceUsecaseAdapter(
		initializeStore InitializeStoreUsecase,
	createBackup CreateStoreBackupUsecase,
	restoreBackup RestoreStoreBackupUsecase,
	collectGarbage CollectGarbageUsecase,
	closeStaleSessions CloseStaleSessionsUsecase,
) StoreMaintenanceUsecase {
	return &storeMaintenanceUsecaseAdapter{
		initializeStore:    initializeStore,
		createBackup:       createBackup,
		restoreBackup:      restoreBackup,
		collectGarbage:     collectGarbage,
		closeStaleSessions: closeStaleSessions,
	}
}

func (a *storeMaintenanceUsecaseAdapter) Initialize(ctx context.Context) error {
	if err := a.initializeStore.Run(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	return nil
}

func (a *storeMaintenanceUsecaseAdapter) CreateBackup(ctx context.Context, outputPath string, overwrite bool) error {
	if err := a.createBackup.Run(ctx, CreateStoreBackupInput{
		OutputPath: outputPath,
		Overwrite:  overwrite,
	}); err != nil {
		return xerrors.Errorf("failed to create backup: %w", err)
	}
	return nil
}

func (a *storeMaintenanceUsecaseAdapter) RestoreBackup(ctx context.Context, inputPath string, overwrite bool) error {
	if err := a.restoreBackup.Run(ctx, RestoreStoreBackupInput{
		InputPath: inputPath,
		Overwrite: overwrite,
	}); err != nil {
		return xerrors.Errorf("failed to restore backup: %w", err)
	}
	return nil
}

func (a *storeMaintenanceUsecaseAdapter) CollectGarbage(ctx context.Context, before time.Time, dryRun bool) (*CollectGarbageResult, error) {
	result, err := a.collectGarbage.Run(ctx, CollectGarbageInput{
		Before: before,
		DryRun: dryRun,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to collect garbage: %w", err)
	}
	return result, nil
}

func (a *storeMaintenanceUsecaseAdapter) CloseStaleSessions(ctx context.Context, staleAfter time.Duration, dryRun bool) (*CloseStaleSessionsResult, error) {
	result, err := a.closeStaleSessions.Run(ctx, CloseStaleSessionsInput{
		StaleAfter: staleAfter,
		DryRun:     dryRun,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to close stale sessions: %w", err)
	}
	return result, nil
}
