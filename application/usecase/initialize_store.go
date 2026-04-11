package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// InitializeStoreUsecase initializes the local store.
type InitializeStoreUsecase interface {
	// Run initializes the local store.
	Run(ctx context.Context) error
}

type initializeStoreUsecase struct {
	storeManager application.StoreManager
}

// NewInitializeStoreUsecase creates an InitializeStoreUsecase.
func NewInitializeStoreUsecase(storeManager application.StoreManager) InitializeStoreUsecase {
	return &initializeStoreUsecase{storeManager: storeManager}
}

// Run initializes the local store.
func (u *initializeStoreUsecase) Run(ctx context.Context) error {
	if err := u.storeManager.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	return nil
}
