//nolint:revive // Public projection API names are intentionally explicit.
package usecase

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// SearchProjectionStore is the narrow persistence boundary for the explicit
// projection state machine. Policy and generation lifecycle stay here.
type SearchProjectionStore interface {
	StartSearchProjectionGeneration(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error)
	RebuildSearchProjections(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionProgress, error)
	SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error)
}

type SearchProjectionUsecase struct{ store SearchProjectionStore }

// NewSearchProjectionUsecase constructs the projection workflow.
func NewSearchProjectionUsecase(store SearchProjectionStore) *SearchProjectionUsecase {
	return &SearchProjectionUsecase{store: store}
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) StartGeneration(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionGeneration, error) {
	return u.store.StartSearchProjectionGeneration(ctx, b, now.UTC())
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) Resume(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	return u.store.RebuildSearchProjections(ctx, b, now.UTC())
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) Inspect(ctx context.Context) (apptypes.SearchProjectionStatus, error) {
	return u.store.SearchProjectionStatus(ctx)
}
