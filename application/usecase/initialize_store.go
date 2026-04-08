package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"
)

// StoreInitializer provides store-initialization behavior.
type StoreInitializer interface {
	// Initialize initializes a store at the given DB path.
	Initialize(ctx context.Context, dbPath string) error
}

// InitializeStoreUsecase initializes the local store.
type InitializeStoreUsecase interface {
	// Run initializes the local store.
	Run(ctx context.Context, dbPath string) error
}

type initializeStoreUsecase struct {
	storeInitializer StoreInitializer
}

// NewInitializeStoreUsecase creates an InitializeStoreUsecase.
func NewInitializeStoreUsecase(storeInitializer StoreInitializer) InitializeStoreUsecase {
	return &initializeStoreUsecase{storeInitializer: storeInitializer}
}

// Run initializes the local store.
func (u *initializeStoreUsecase) Run(ctx context.Context, dbPath string) error {
	trimmedPath := strings.TrimSpace(dbPath)
	if trimmedPath == "" {
		return xerrors.Errorf("DB path must not be empty")
	}
	if err := u.storeInitializer.Initialize(ctx, trimmedPath); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	return nil
}
