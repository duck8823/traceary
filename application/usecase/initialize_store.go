package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

// StoreInitializer is defined in domain/port.
type StoreInitializer = port.StoreInitializer

// InitializeStoreUsecase initializes the local store.
type InitializeStoreUsecase interface {
	// Run initializes the local store.
	Run(ctx context.Context) error
}

type initializeStoreUsecase struct {
	storeInitializer StoreInitializer
}

// NewInitializeStoreUsecase creates an InitializeStoreUsecase.
func NewInitializeStoreUsecase(storeInitializer StoreInitializer) InitializeStoreUsecase {
	return &initializeStoreUsecase{storeInitializer: storeInitializer}
}

// Run initializes the local store.
func (u *initializeStoreUsecase) Run(ctx context.Context) error {
	if err := u.storeInitializer.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	return nil
}
