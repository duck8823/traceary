package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// UsageObservationUsecase is the write boundary consumed by host usage
// adapters. Domain and repository layers own validity and idempotency.
type UsageObservationUsecase interface {
	Record(ctx context.Context, observation *model.UsageObservation) (model.UsageObservationTransition, error)
}

type usageObservationUsecase struct {
	repository model.UsageObservationRepository
}

// NewUsageObservationUsecase creates the provider-neutral usage write usecase.
func NewUsageObservationUsecase(repository model.UsageObservationRepository) UsageObservationUsecase {
	return &usageObservationUsecase{repository: repository}
}

func (u *usageObservationUsecase) Record(
	ctx context.Context,
	observation *model.UsageObservation,
) (model.UsageObservationTransition, error) {
	if u.repository == nil {
		return "", xerrors.Errorf("usage observation repository is not configured")
	}
	transition, err := u.repository.Record(ctx, observation)
	if err != nil {
		return transition, xerrors.Errorf("failed to record usage observation: %w", err)
	}
	return transition, nil
}
