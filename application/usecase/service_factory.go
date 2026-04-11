package usecase

import "context"

// Services holds the consolidated usecase instances.
type Services struct {
	Event            EventUsecase
	Session          SessionUsecase
	StoreMaintenance StoreMaintenanceUsecase
}

// ServiceFactory builds Services for a given database path.
type ServiceFactory interface {
	// Build creates all usecase instances bound to the given dbPath.
	Build(ctx context.Context, dbPath string) (*Services, error)
}
