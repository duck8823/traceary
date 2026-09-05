package usecase_test

import (
	"context"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func TestEventBoundedUsecase_ListLoadsBlocksOnlyForCanonicalUntruncatedRows(t *testing.T) {
	t.Parallel()

	canonical := boundedUsecaseEventFixture(t, "canonical", "visible", 7, true)
	truncated := boundedUsecaseEventFixture(t, "truncated", "visible", 10, true)
	plain := boundedUsecaseEventFixture(t, "plain", "visible", 7, false)
	query := &eventBoundedQueryStub{
		list: []apptypes.BoundedEvent{canonical, truncated, plain},
		canonicalBodies: map[types.EventID]string{
			types.EventID("canonical"): `{"blocks":[{"type":"thinking","text":"hidden"},{"type":"text","text":"visible"}]}`,
		},
	}
	sut := usecase.NewEventBoundedUsecase(query)

	got, err := sut.List(
		context.Background(),
		apptypes.NewEventListCriteriaBuilder(3).Build(),
		7,
	)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(query.loadedCanonicalIDs) != 1 || query.loadedCanonicalIDs[0] != types.EventID("canonical") {
		t.Fatalf("LoadCanonicalBodies() IDs = %v, want canonical only", query.loadedCanonicalIDs)
	}
	if len(got) != 3 || len(got[0].BodyBlocks()) != 2 {
		t.Fatalf("List() bounded events = %+v", got)
	}
	if len(got[1].BodyBlocks()) != 0 || len(got[2].BodyBlocks()) != 0 {
		t.Fatalf("non-fallback blocks = truncated:%+v plain:%+v", got[1].BodyBlocks(), got[2].BodyBlocks())
	}
}

func TestEventBoundedUsecase_ContextNeverLoadCanonicalBodies(t *testing.T) {
	t.Parallel()

	canonical := boundedUsecaseEventFixture(t, "canonical", "visible", 7, true)
	query := &eventBoundedQueryStub{
		context: []apptypes.BoundedEvent{canonical},
		canonicalBodies: map[types.EventID]string{
			types.EventID("canonical"): `{"blocks":[{"type":"text","text":"visible"}]}`,
		},
	}
	sut := usecase.NewEventBoundedUsecase(query)

	contextEvents, err := sut.Context(
		context.Background(),
		apptypes.NewEventContextCriteriaBuilder(1).SessionID("session-1").Build(),
		7,
	)
	if err != nil {
		t.Fatalf("Context() error = %v", err)
	}
	if len(contextEvents) != 1 {
		t.Fatalf("Context() len = %d, want 1", len(contextEvents))
	}
	if len(query.loadedCanonicalIDs) != 0 {
		t.Fatalf("LoadCanonicalBodies() IDs = %v, want none", query.loadedCanonicalIDs)
	}
}

func TestEventBoundedUsecase_RejectsUnboundedBodyLimitBeforeQuery(t *testing.T) {
	t.Parallel()

	query := &eventBoundedQueryStub{}
	sut := usecase.NewEventBoundedUsecase(query)
	if _, err := sut.List(
		context.Background(),
		apptypes.NewEventListCriteriaBuilder(1).Build(),
		0,
	); err == nil {
		t.Fatal("List(bodyRuneLimit=0) error = nil")
	}
	if query.listCalls != 0 {
		t.Fatalf("ListRecentBounded() calls = %d, want 0", query.listCalls)
	}
}

func TestEventBoundedUsecase_HydratesExactMetadataSelection(t *testing.T) {
	t.Parallel()

	first := boundedUsecaseEventFixture(t, "first", "first body", 10, false)
	second := boundedUsecaseEventFixture(t, "second", "second body", 11, false)
	query := &eventBoundedQueryStub{
		hydrated: []apptypes.BoundedEvent{first, second},
	}
	sut := usecase.NewEventBoundedUsecase(query)
	metadata := []apptypes.EventMetadata{first.Metadata(), second.Metadata()}

	got, err := sut.HydrateSearch(context.Background(), metadata, 100)
	if err != nil {
		t.Fatalf("HydrateSearch() error = %v", err)
	}
	if len(got) != 2 || query.hydrateCalls != 1 {
		t.Fatalf("HydrateSearch() result/calls = %d/%d, want 2/1", len(got), query.hydrateCalls)
	}
	if len(query.hydratedMetadata) != 2 ||
		query.hydratedMetadata[0].EventID() != first.Metadata().EventID() ||
		query.hydratedMetadata[1].EventID() != second.Metadata().EventID() {
		t.Fatalf("HydrateBounded() metadata = %+v, want exact selected order", query.hydratedMetadata)
	}
}

func TestEventBoundedUsecase_RejectsHydrationThatChangesCandidateOrder(t *testing.T) {
	t.Parallel()

	first := boundedUsecaseEventFixture(t, "first", "first body", 10, false)
	second := boundedUsecaseEventFixture(t, "second", "second body", 11, false)
	query := &eventBoundedQueryStub{
		hydrated: []apptypes.BoundedEvent{second, first},
	}
	sut := usecase.NewEventBoundedUsecase(query)
	metadata := []apptypes.EventMetadata{first.Metadata(), second.Metadata()}

	if _, err := sut.HydrateContext(context.Background(), metadata, 100); err == nil {
		t.Fatal("HydrateContext() accepted reordered candidates")
	}
}

type eventBoundedQueryStub struct {
	list               []apptypes.BoundedEvent
	context            []apptypes.BoundedEvent
	hydrated           []apptypes.BoundedEvent
	canonicalBodies    map[types.EventID]string
	loadedCanonicalIDs []types.EventID
	hydratedMetadata   []apptypes.EventMetadata
	listCalls          int
	hydrateCalls       int
}

func (s *eventBoundedQueryStub) ListRecentBounded(
	context.Context,
	apptypes.EventListCriteria,
	int,
) ([]apptypes.BoundedEvent, error) {
	s.listCalls++
	return s.list, nil
}

func (s *eventBoundedQueryStub) GetContextBounded(
	context.Context,
	apptypes.EventContextCriteria,
	int,
) ([]apptypes.BoundedEvent, error) {
	return s.context, nil
}

func (s *eventBoundedQueryStub) HydrateBounded(
	_ context.Context,
	metadata []apptypes.EventMetadata,
	_ int,
) ([]apptypes.BoundedEvent, error) {
	s.hydrateCalls++
	s.hydratedMetadata = append([]apptypes.EventMetadata(nil), metadata...)
	return s.hydrated, nil
}

func (s *eventBoundedQueryStub) LoadCanonicalBodies(
	_ context.Context,
	eventIDs []types.EventID,
) (map[types.EventID]string, error) {
	s.loadedCanonicalIDs = append([]types.EventID(nil), eventIDs...)
	return s.canonicalBodies, nil
}

func boundedUsecaseEventFixture(
	t *testing.T,
	eventID string,
	body string,
	visibleRunes int,
	canonical bool,
) apptypes.BoundedEvent {
	t.Helper()
	extent, err := apptypes.EventBodyExtentOf(
		types.None[int](),
		len(body),
		types.None[bool](),
		types.None[bool](),
		types.None[int](),
	)
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}
	metadata, err := apptypes.EventMetadataOf(
		types.EventID(eventID),
		types.EventKindTranscript,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID("session-1"),
		types.Workspace("duck8823/traceary"),
		"stop",
		time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		extent,
		types.None[apptypes.CommandAuditMetadata](),
	)
	if err != nil {
		t.Fatalf("EventMetadataOf() error = %v", err)
	}
	event, err := apptypes.BoundedEventOf(
		metadata,
		body,
		len([]rune(body)),
		visibleRunes,
		canonical,
	)
	if err != nil {
		t.Fatalf("BoundedEventOf() error = %v", err)
	}
	return event
}
