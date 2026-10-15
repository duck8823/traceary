package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
	windowCalls    int
	windowCriteria types.EventListCriteria
	windowRows     []types.EventMetadata
	windowErr      error
	listCalls      int
	searchCalls    int
	contextCalls   int
	searchCriteria types.EventSearchCriteria
}

func (*eventMetadataQueryStub) ListRecentTimestampKinds(context.Context, types.EventListCriteria) ([]types.EventTimestampKind, error) {
	return nil, nil
}

func (s *eventMetadataQueryStub) ListRecentMetadata(context.Context, types.EventListCriteria) ([]types.EventMetadata, error) {
	s.listCalls++
	return nil, nil
}
func (s *eventMetadataQueryStub) ListWindowMetadata(_ context.Context, criteria types.EventListCriteria) ([]types.EventMetadata, error) {
	s.windowCalls++
	s.windowCriteria = criteria
	return s.windowRows, s.windowErr
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

func TestEventMetadataUsecase_ListWindow(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	row, err := types.EventMetadataOf("event-1", domtypes.EventKindNote, "cli", "codex", "session-1", "workspace-1", "", at, types.EventBodyExtent{}, domtypes.None[types.CommandAuditMetadata]())
	if err != nil {
		t.Fatal(err)
	}
	query := &eventMetadataQueryStub{windowRows: []types.EventMetadata{row}}
	sut := usecase.NewEventMetadataUsecase(query)
	criteria := types.NewEventListCriteriaBuilder(100).From(at).To(at.Add(time.Minute)).Workspace("workspace-1").Build()
	rows, err := sut.ListWindow(context.Background(), criteria)
	if err != nil || len(rows) != 1 || rows[0].EventID() != row.EventID() {
		t.Fatalf("ListWindow() = %v, %v", rows, err)
	}
	if query.windowCalls != 1 || query.windowCriteria != criteria {
		t.Fatalf("metadata window call = %#v", query)
	}
	query.windowErr = errors.New("query failed")
	if _, err := sut.ListWindow(context.Background(), criteria); !errors.Is(err, query.windowErr) {
		t.Fatalf("ListWindow() error = %v, want query cause", err)
	}
}

func TestEventMetadataUsecase_ListWindowValidatesBeforeQuery(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name     string
		criteria types.EventListCriteria
	}{
		{"zero limit", types.NewEventListCriteriaBuilder(0).Build()},
		{"negative limit", types.NewEventListCriteriaBuilder(-1).Build()},
		{"positive offset", types.NewEventListCriteriaBuilder(10).Offset(1).Build()},
		{"negative offset", types.NewEventListCriteriaBuilder(10).Offset(-1).Build()},
		{"reversed time bounds", types.NewEventListCriteriaBuilder(10).From(at.Add(time.Minute)).To(at).Build()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			query := &eventMetadataQueryStub{}
			if _, err := usecase.NewEventMetadataUsecase(query).ListWindow(context.Background(), tt.criteria); err == nil {
				t.Fatal("ListWindow() error = nil")
			}
			if query.windowCalls != 0 {
				t.Fatal("invalid criteria reached query")
			}
		})
	}
	if _, err := usecase.NewEventMetadataUsecase(nil).ListWindow(context.Background(), types.NewEventListCriteriaBuilder(10).Build()); err == nil {
		t.Fatal("missing query accepted")
	}
}
