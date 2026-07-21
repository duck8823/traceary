package usecase_test

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestEventMetadataUsecase_SearchResolvesAuditAlias(t *testing.T) {
	t.Parallel()

	query := &eventMetadataQueryStub{}
	sut := usecase.NewEventMetadataUsecase(query)
	criteria := types.NewEventSearchCriteriaBuilder(10).
		Query("failed").
		Kind(domtypes.EventKind("audit")).
		Build()
	if _, err := sut.Search(context.Background(), criteria); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if query.searchCriteria.Kind() != domtypes.EventKindCommandExecuted {
		t.Fatalf("SearchMetadata() kind = %q, want %q", query.searchCriteria.Kind(), domtypes.EventKindCommandExecuted)
	}
}

func TestEventMetadataUsecase_ValidatesBeforeQuery(t *testing.T) {
	t.Parallel()

	query := &eventMetadataQueryStub{}
	sut := usecase.NewEventMetadataUsecase(query)
	if _, err := sut.List(context.Background(), types.NewEventListCriteriaBuilder(0).Build()); err == nil {
		t.Fatal("List() error = nil, want validation error")
	}
	if query.listCalls != 0 {
		t.Fatalf("ListRecentMetadata() calls = %d, want 0", query.listCalls)
	}
	if _, err := sut.Search(context.Background(), types.NewEventSearchCriteriaBuilder(10).Build()); err == nil {
		t.Fatal("Search() error = nil, want validation error")
	}
	if query.searchCalls != 0 {
		t.Fatalf("SearchMetadata() calls = %d, want 0", query.searchCalls)
	}
}

type eventMetadataQueryStub struct {
	listCalls      int
	searchCalls    int
	contextCalls   int
	searchCriteria types.EventSearchCriteria
}

func (s *eventMetadataQueryStub) ListRecentMetadata(context.Context, types.EventListCriteria) ([]types.EventMetadata, error) {
	s.listCalls++
	return nil, nil
}
func (*eventMetadataQueryStub) ListWindowMetadata(context.Context, types.EventListCriteria) ([]types.EventMetadata, error) {
	return nil, nil
}
func (s *eventMetadataQueryStub) SearchMetadata(_ context.Context, criteria types.EventSearchCriteria) ([]types.EventMetadata, error) {
	s.searchCalls++
	s.searchCriteria = criteria
	return nil, nil
}
func (s *eventMetadataQueryStub) GetContextMetadata(context.Context, types.EventContextCriteria) ([]types.EventMetadata, error) {
	s.contextCalls++
	return nil, nil
}
