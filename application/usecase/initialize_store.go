package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"
)

// StoreInitializer はストア初期化処理を提供するインターフェースです。
type StoreInitializer interface {
	// Initialize は指定された DB パスにストアを初期化します。
	Initialize(ctx context.Context, dbPath string) error
}

// InitializeStoreUsecase はローカルストア初期化のユースケースです。
type InitializeStoreUsecase interface {
	// Run はローカルストアを初期化します。
	Run(ctx context.Context, dbPath string) error
}

type initializeStoreUsecase struct {
	storeInitializer StoreInitializer
}

// NewInitializeStoreUsecase はストア初期化ユースケースを生成します。
func NewInitializeStoreUsecase(storeInitializer StoreInitializer) InitializeStoreUsecase {
	return &initializeStoreUsecase{storeInitializer: storeInitializer}
}

// Run はローカルストアを初期化します。
func (u *initializeStoreUsecase) Run(ctx context.Context, dbPath string) error {
	trimmedPath := strings.TrimSpace(dbPath)
	if trimmedPath == "" {
		return xerrors.Errorf("DB パスは空にできません")
	}
	if err := u.storeInitializer.Initialize(ctx, trimmedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}
	return nil
}
