package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// CreateStoreBackupInput is the input for backup creation.
type CreateStoreBackupInput struct {
	OutputPath string
	Overwrite  bool
}

// RestoreStoreBackupInput is the input for backup restoration.
type RestoreStoreBackupInput struct {
	InputPath string
	Overwrite bool
}

// CreateStoreBackupUsecase creates Traceary store backups.
type CreateStoreBackupUsecase interface {
	// Run creates a backup file from a DB file.
	Run(ctx context.Context, input CreateStoreBackupInput) error
}

// RestoreStoreBackupUsecase restores the Traceary store from a backup.
type RestoreStoreBackupUsecase interface {
	// Run restores a DB file from a backup file.
	Run(ctx context.Context, input RestoreStoreBackupInput) error
}

type createStoreBackupUsecase struct {
	storeManager application.StoreManager
}

type restoreStoreBackupUsecase struct {
	storeManager application.StoreManager
}

// NewCreateStoreBackupUsecase creates a CreateStoreBackupUsecase.
func NewCreateStoreBackupUsecase(storeManager application.StoreManager) CreateStoreBackupUsecase {
	return &createStoreBackupUsecase{storeManager: storeManager}
}

// NewRestoreStoreBackupUsecase creates a RestoreStoreBackupUsecase.
func NewRestoreStoreBackupUsecase(storeManager application.StoreManager) RestoreStoreBackupUsecase {
	return &restoreStoreBackupUsecase{storeManager: storeManager}
}

// Run creates a backup.
func (u *createStoreBackupUsecase) Run(ctx context.Context, input CreateStoreBackupInput) error {
	if u.storeManager == nil {
		return xerrors.Errorf("store manager is not configured")
	}
	if strings.TrimSpace(input.OutputPath) == "" {
		return xerrors.Errorf("output path must not be empty")
	}
	if err := u.storeManager.CreateBackup(ctx, strings.TrimSpace(input.OutputPath), input.Overwrite); err != nil {
		return xerrors.Errorf("failed to create store backup: %w", err)
	}

	return nil
}

// Run restores from a backup.
func (u *restoreStoreBackupUsecase) Run(ctx context.Context, input RestoreStoreBackupInput) error {
	if u.storeManager == nil {
		return xerrors.Errorf("store manager is not configured")
	}
	if strings.TrimSpace(input.InputPath) == "" {
		return xerrors.Errorf("input path must not be empty")
	}
	if err := u.storeManager.RestoreBackup(ctx, strings.TrimSpace(input.InputPath), input.Overwrite); err != nil {
		return xerrors.Errorf("failed to restore store backup: %w", err)
	}

	return nil
}
