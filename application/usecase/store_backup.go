package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"
)

// StoreBackupCreator は Traceary ストアのバックアップ作成を提供します。
type StoreBackupCreator interface {
	// CreateBackup は DB ファイルからバックアップファイルを作成します。
	CreateBackup(ctx context.Context, dbPath string, outputPath string, overwrite bool) error
}

// StoreBackupRestorer は Traceary ストアの復元を提供します。
type StoreBackupRestorer interface {
	// RestoreBackup はバックアップファイルから DB ファイルを復元します。
	RestoreBackup(ctx context.Context, inputPath string, dbPath string, overwrite bool) error
}

// CreateStoreBackupInput はバックアップ作成の入力です。
type CreateStoreBackupInput struct {
	DBPath     string
	OutputPath string
	Overwrite  bool
}

// RestoreStoreBackupInput はバックアップ復元の入力です。
type RestoreStoreBackupInput struct {
	DBPath    string
	InputPath string
	Overwrite bool
}

// CreateStoreBackupUsecase は Traceary ストアのバックアップを作成します。
type CreateStoreBackupUsecase interface {
	// Run は DB ファイルからバックアップファイルを作成します。
	Run(ctx context.Context, input CreateStoreBackupInput) error
}

// RestoreStoreBackupUsecase は Traceary ストアをバックアップから復元します。
type RestoreStoreBackupUsecase interface {
	// Run はバックアップファイルから DB ファイルを復元します。
	Run(ctx context.Context, input RestoreStoreBackupInput) error
}

type createStoreBackupUsecase struct {
	storeBackupCreator StoreBackupCreator
}

type restoreStoreBackupUsecase struct {
	storeBackupRestorer StoreBackupRestorer
}

// NewCreateStoreBackupUsecase は新しいバックアップ作成ユースケースを返します。
func NewCreateStoreBackupUsecase(storeBackupCreator StoreBackupCreator) CreateStoreBackupUsecase {
	return &createStoreBackupUsecase{storeBackupCreator: storeBackupCreator}
}

// NewRestoreStoreBackupUsecase は新しいバックアップ復元ユースケースを返します。
func NewRestoreStoreBackupUsecase(storeBackupRestorer StoreBackupRestorer) RestoreStoreBackupUsecase {
	return &restoreStoreBackupUsecase{storeBackupRestorer: storeBackupRestorer}
}

// Run はバックアップを作成します。
func (u *createStoreBackupUsecase) Run(ctx context.Context, input CreateStoreBackupInput) error {
	if u.storeBackupCreator == nil {
		return xerrors.Errorf("バックアップ作成先が設定されていません")
	}
	if strings.TrimSpace(input.DBPath) == "" {
		return xerrors.Errorf("DB パスは空にできません")
	}
	if strings.TrimSpace(input.OutputPath) == "" {
		return xerrors.Errorf("出力先パスは空にできません")
	}
	if err := u.storeBackupCreator.CreateBackup(ctx, strings.TrimSpace(input.DBPath), strings.TrimSpace(input.OutputPath), input.Overwrite); err != nil {
		return xerrors.Errorf("バックアップ作成に失敗しました: %w", err)
	}

	return nil
}

// Run はバックアップから復元します。
func (u *restoreStoreBackupUsecase) Run(ctx context.Context, input RestoreStoreBackupInput) error {
	if u.storeBackupRestorer == nil {
		return xerrors.Errorf("バックアップ復元先が設定されていません")
	}
	if strings.TrimSpace(input.DBPath) == "" {
		return xerrors.Errorf("DB パスは空にできません")
	}
	if strings.TrimSpace(input.InputPath) == "" {
		return xerrors.Errorf("入力ファイルパスは空にできません")
	}
	if err := u.storeBackupRestorer.RestoreBackup(ctx, strings.TrimSpace(input.InputPath), strings.TrimSpace(input.DBPath), input.Overwrite); err != nil {
		return xerrors.Errorf("バックアップ復元に失敗しました: %w", err)
	}

	return nil
}
