package usecase_test

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

func TestEventUsecase_UsesRequiredPageQueryContract(t *testing.T) {
	t.Parallel()

	query := &eventReadQueryStub{}
	sut := usecase.NewEventUsecase(nil, query)
	if _, err := sut.List(
		context.Background(),
		apptypes.NewEventListCriteriaBuilder(1).Build(),
	); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, err := sut.Search(
		context.Background(),
		apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Build(),
	); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if _, err := sut.Context(
		context.Background(),
		apptypes.NewEventContextCriteriaBuilder(1).Build(),
	); err != nil {
		t.Fatalf("Context() error = %v", err)
	}
	if query.listPageCalls != 1 || query.searchPageCalls != 1 || query.contextPageCalls != 1 {
		t.Fatalf(
			"page query calls = list:%d search:%d context:%d, want one each",
			query.listPageCalls, query.searchPageCalls, query.contextPageCalls,
		)
	}
}

type eventReadQueryStub struct {
	// The embedded legacy contract is intentionally nil. A runtime fallback to
	// one of its methods panics, while the explicitly implemented page methods
	// below remain observable.
	queryservice.EventQueryService
	listPageCalls    int
	searchPageCalls  int
	contextPageCalls int
}

func (s *eventReadQueryStub) ListRecentPage(
	context.Context,
	apptypes.EventListCriteria,
) ([]*model.Event, error) {
	s.listPageCalls++
	return []*model.Event{}, nil
}

func (s *eventReadQueryStub) SearchPage(
	context.Context,
	apptypes.EventSearchCriteria,
) ([]*model.Event, error) {
	s.searchPageCalls++
	return []*model.Event{}, nil
}

func (s *eventReadQueryStub) GetContextPage(
	context.Context,
	apptypes.EventContextCriteria,
) ([]*model.Event, error) {
	s.contextPageCalls++
	return []*model.Event{}, nil
}
