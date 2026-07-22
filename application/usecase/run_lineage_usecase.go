package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// RunLineageUsecase is the write boundary consumed by host adapters.
type RunLineageUsecase interface {
	Record(ctx context.Context, lineage *model.RunLineage) (model.RunLineageTransition, error)
}

type runLineageUsecase struct{ repository model.RunLineageRepository }

// NewRunLineageUsecase creates a provider-neutral lineage write usecase.
func NewRunLineageUsecase(repository model.RunLineageRepository) RunLineageUsecase {
	return &runLineageUsecase{repository: repository}
}

func (u *runLineageUsecase) Record(ctx context.Context, lineage *model.RunLineage) (model.RunLineageTransition, error) {
	if u.repository == nil {
		return "", xerrors.Errorf("run lineage repository is not configured")
	}
	transition, err := u.repository.Record(ctx, lineage)
	if err != nil {
		return transition, xerrors.Errorf("failed to record run lineage: %w", err)
	}
	return transition, nil
}
