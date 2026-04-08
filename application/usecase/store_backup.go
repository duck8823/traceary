package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"
)

// StoreBackupCreator provides Traceary store backup creation.
type StoreBackupCreator interface {
	// CreateBackup creates a backup file from a DB file.
	CreateBackup(ctx context.Context, dbPath string, outputPath string, overwrite bool) error
}

// StoreBackupRestorer provides Traceary store restoration.
type StoreBackupRestorer interface {
	// RestoreBackup restores a DB file from a backup file.
	RestoreBackup(ctx context.Context, inputPath string, dbPath string, overwrite bool) error
}

// CreateStoreBackupInput is the input for backup creation.
type CreateStoreBackupInput struct {
	DBPath     string
	OutputPath string
	Overwrite  bool
}

// RestoreStoreBackupInput is the input for backup restoration.
type RestoreStoreBackupInput struct {
	DBPath    string
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
	storeBackupCreator StoreBackupCreator
}

type restoreStoreBackupUsecase struct {
	storeBackupRestorer StoreBackupRestorer
}

// NewCreateStoreBackupUsecase creates a CreateStoreBackupUsecase.
func NewCreateStoreBackupUsecase(storeBackupCreator StoreBackupCreator) CreateStoreBackupUsecase {
	return &createStoreBackupUsecase{storeBackupCreator: storeBackupCreator}
}

// NewRestoreStoreBackupUsecase creates a RestoreStoreBackupUsecase.
func NewRestoreStoreBackupUsecase(storeBackupRestorer StoreBackupRestorer) RestoreStoreBackupUsecase {
	return &restoreStoreBackupUsecase{storeBackupRestorer: storeBackupRestorer}
}

// Run creates a backup.
func (u *createStoreBackupUsecase) Run(ctx context.Context, input CreateStoreBackupInput) error {
	if u.storeBackupCreator == nil {
		return xerrors.Errorf("store backup creator is not configured")
	}
	if strings.TrimSpace(input.DBPath) == "" {
		return xerrors.Errorf("DB path must not be empty")
	}
	if strings.TrimSpace(input.OutputPath) == "" {
		return xerrors.Errorf("output path must not be empty")
	}
	if err := u.storeBackupCreator.CreateBackup(ctx, strings.TrimSpace(input.DBPath), strings.TrimSpace(input.OutputPath), input.Overwrite); err != nil {
		return xerrors.Errorf("failed to create store backup: %w", err)
	}

	return nil
}

// Run restores from a backup.
func (u *restoreStoreBackupUsecase) Run(ctx context.Context, input RestoreStoreBackupInput) error {
	if u.storeBackupRestorer == nil {
		return xerrors.Errorf("store backup restorer is not configured")
	}
	if strings.TrimSpace(input.DBPath) == "" {
		return xerrors.Errorf("DB path must not be empty")
	}
	if strings.TrimSpace(input.InputPath) == "" {
		return xerrors.Errorf("input path must not be empty")
	}
	if err := u.storeBackupRestorer.RestoreBackup(ctx, strings.TrimSpace(input.InputPath), strings.TrimSpace(input.DBPath), input.Overwrite); err != nil {
		return xerrors.Errorf("failed to restore store backup: %w", err)
	}

	return nil
}
