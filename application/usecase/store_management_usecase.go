package usecase

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
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
	CollectGarbage(ctx context.Context, before time.Time, dryRun bool) (*CollectGarbageResult, error)

	// CloseStaleSessions closes sessions active beyond the given duration.
	CloseStaleSessions(ctx context.Context, staleAfter time.Duration, dryRun bool) (*CloseStaleSessionsResult, error)
}

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

// CollectGarbageInput is the input for garbage collection.
type CollectGarbageInput struct {
	Before time.Time
	DryRun bool
}

// CollectGarbageResult is the result of a garbage-collection run.
type CollectGarbageResult struct {
	DeletedCount int
	Before       time.Time
	DryRun       bool
}

// CloseStaleSessionsInput is the input for closing stale sessions.
type CloseStaleSessionsInput struct {
	StaleAfter time.Duration
	DryRun     bool
}

// CloseStaleSessionsResult is the result of a stale-session cleanup.
type CloseStaleSessionsResult struct {
	ClosedCount int
}

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
	dryRun bool,
) (*CollectGarbageResult, error) {
	if before.IsZero() {
		return nil, xerrors.Errorf("before timestamp is required")
	}

	deletedCount, err := u.storeManager.CollectGarbage(ctx, before, dryRun)
	if err != nil {
		return nil, xerrors.Errorf("failed to collect garbage: %w", err)
	}

	return &CollectGarbageResult{
		DeletedCount: deletedCount,
		Before:       before,
		DryRun:       dryRun,
	}, nil
}

func (u *storeManagementUsecase) CloseStaleSessions(
	ctx context.Context,
	staleAfter time.Duration,
	dryRun bool,
) (*CloseStaleSessionsResult, error) {
	closedCount, err := u.storeManager.CloseStaleSessions(ctx, staleAfter, dryRun)
	if err != nil {
		return nil, xerrors.Errorf("failed to close stale sessions: %w", err)
	}

	return &CloseStaleSessionsResult{
		ClosedCount: closedCount,
	}, nil
}
