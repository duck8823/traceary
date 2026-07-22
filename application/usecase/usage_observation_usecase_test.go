package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestUsageObservationUsecase_RecordDelegatesTransitionAndPreservesConflict(t *testing.T) {
	t.Parallel()

	repo := &usageObservationRepositoryStub{
		transition: model.UsageObservationTransitionAlreadyApplied,
		err:        model.ErrConflictingUsageObservation,
	}
	sut := usecase.NewUsageObservationUsecase(repo)

	transition, err := sut.Record(context.Background(), &model.UsageObservation{})
	if transition != model.UsageObservationTransitionAlreadyApplied {
		t.Fatalf("Record() transition = %q", transition)
	}
	if !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("Record() error = %v", err)
	}
	if !repo.called {
		t.Fatal("repository was not called")
	}
}

func TestUsageObservationUsecase_RecordRejectsMissingRepository(t *testing.T) {
	t.Parallel()

	sut := usecase.NewUsageObservationUsecase(nil)
	if _, err := sut.Record(context.Background(), &model.UsageObservation{}); err == nil {
		t.Fatal("Record() error = nil")
	}
}

type usageObservationRepositoryStub struct {
	called     bool
	transition model.UsageObservationTransition
	err        error
}

func (s *usageObservationRepositoryStub) Record(context.Context, *model.UsageObservation) (model.UsageObservationTransition, error) {
	s.called = true
	return s.transition, s.err
}

func (s *usageObservationRepositoryStub) FindByID(context.Context, types.UsageObservationID) (types.Optional[*model.UsageObservation], error) {
	return types.None[*model.UsageObservation](), nil
}
