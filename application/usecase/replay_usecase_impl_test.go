package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type stubReplaySessionUsecase struct {
	sessions []apptypes.SessionSummary
}

func (s *stubReplaySessionUsecase) Start(context.Context, domtypes.Client, domtypes.Agent, domtypes.SessionID, domtypes.Workspace, domtypes.SessionID) (*model.Event, error) {
	return nil, nil
}
func (s *stubReplaySessionUsecase) End(context.Context, domtypes.Client, domtypes.Agent, domtypes.SessionID, domtypes.Workspace, string) (*model.Event, error) {
	return nil, nil
}
func (s *stubReplaySessionUsecase) Label(context.Context, domtypes.SessionID, string) error {
	return nil
}
func (s *stubReplaySessionUsecase) List(context.Context, apptypes.SessionListCriteria) ([]apptypes.SessionSummary, error) {
	return s.sessions, nil
}
func (s *stubReplaySessionUsecase) Tree(context.Context, domtypes.Workspace, int) ([]apptypes.SessionSummary, error) {
	return nil, nil
}
func (s *stubReplaySessionUsecase) Active(context.Context, apptypes.SessionLookupCriteria) (domtypes.Optional[*model.Event], error) {
	return domtypes.None[*model.Event](), nil
}
func (s *stubReplaySessionUsecase) Latest(context.Context, apptypes.SessionLookupCriteria) (domtypes.Optional[*model.Event], error) {
	return domtypes.None[*model.Event](), nil
}
func (s *stubReplaySessionUsecase) Handoff(context.Context, domtypes.SessionID, domtypes.Workspace, int) (domtypes.Optional[apptypes.HandoffSummary], error) {
	return domtypes.None[apptypes.HandoffSummary](), nil
}

type stubReplayEventUsecase struct {
	eventsBySession map[domtypes.SessionID][]*model.Event
}

func (s *stubReplayEventUsecase) Log(context.Context, string, domtypes.EventKind, domtypes.Client, domtypes.Agent, domtypes.SessionID, domtypes.Workspace) (*model.Event, error) {
	return nil, nil
}
func (s *stubReplayEventUsecase) Audit(context.Context, string, string, string, domtypes.Client, domtypes.Agent, domtypes.SessionID, domtypes.Workspace, domtypes.Optional[int], apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	return nil, nil, nil
}
func (s *stubReplayEventUsecase) Search(context.Context, apptypes.EventSearchCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (s *stubReplayEventUsecase) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	return s.eventsBySession[criteria.SessionID()], nil
}
func (s *stubReplayEventUsecase) ListWindow(context.Context, apptypes.EventListCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (s *stubReplayEventUsecase) Show(context.Context, domtypes.EventID) (apptypes.EventDetails, error) {
	return apptypes.EventDetails{}, nil
}
func (s *stubReplayEventUsecase) Context(context.Context, apptypes.EventContextCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (s *stubReplayEventUsecase) Timeline(context.Context, apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	return nil, nil
}

type stubReplayMemoryUsecase struct {
	memories     []apptypes.MemorySummary
	lastCriteria apptypes.MemoryListCriteria
	called       bool
}

func (s *stubReplayMemoryUsecase) Remember(context.Context, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.Optional[domtypes.Confidence], domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}
func (s *stubReplayMemoryUsecase) Propose(context.Context, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}
func (s *stubReplayMemoryUsecase) Accept(context.Context, domtypes.MemoryID, domtypes.Optional[domtypes.Confidence]) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}
func (s *stubReplayMemoryUsecase) Reject(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}
func (s *stubReplayMemoryUsecase) Supersede(context.Context, domtypes.MemoryID, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.Optional[domtypes.Confidence], domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}
func (s *stubReplayMemoryUsecase) Expire(context.Context, domtypes.MemoryID, domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}
func (s *stubReplayMemoryUsecase) SetValidity(context.Context, domtypes.MemoryID, domtypes.Optional[time.Time], domtypes.Optional[time.Time], bool) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}
func (s *stubReplayMemoryUsecase) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.called = true
	s.lastCriteria = criteria
	return s.memories, nil
}
func (s *stubReplayMemoryUsecase) Search(context.Context, apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}
func (s *stubReplayMemoryUsecase) Show(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func sessionSummary(t *testing.T, id string, workspace string) apptypes.SessionSummary {
	t.Helper()
	ws, err := domtypes.WorkspaceOf(workspace)
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	return apptypes.SessionSummaryOf(
		domtypes.SessionID(id),
		ws,
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		domtypes.None[time.Time](),
		"active",
		0, 0, nil, "", "", domtypes.SessionID(""),
	)
}

func memorySummary(t *testing.T, id string, workspace string, fact string) apptypes.MemorySummary {
	t.Helper()
	ws, err := domtypes.WorkspaceOf(workspace)
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(ws)
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		scope,
		fact,
		domtypes.MemoryStatusAccepted,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		now, domtypes.None[time.Time](),
		now, now,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}

func TestReplayUsecase_Bundle_ScopesMemoryBySessionWorkspaces(t *testing.T) {
	t.Parallel()

	session := &stubReplaySessionUsecase{sessions: []apptypes.SessionSummary{
		sessionSummary(t, "sess-1", "github.com/example/a"),
		sessionSummary(t, "sess-2", "github.com/example/b"),
	}}
	event := &stubReplayEventUsecase{eventsBySession: map[domtypes.SessionID][]*model.Event{}}
	memory := &stubReplayMemoryUsecase{memories: []apptypes.MemorySummary{
		memorySummary(t, "mem-a", "github.com/example/a", "a fact"),
	}}
	uc := usecase.NewReplayUsecase(session, event, memory)

	bundle, err := uc.Bundle(context.Background(), apptypes.NewReplayCriteriaBuilder(10, 20, 20).Build())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if len(bundle.Sessions()) != 2 {
		t.Fatalf("Sessions count = %d, want 2", len(bundle.Sessions()))
	}
	if !memory.called {
		t.Fatalf("memory.List was not called")
	}
	scopes := memory.lastCriteria.Scopes()
	if len(scopes) != 2 {
		t.Fatalf("memory criteria scope count = %d, want 2 (one per session workspace)", len(scopes))
	}
}

func TestReplayUsecase_Bundle_SkipsMemoryPanelWhenNoWorkspaces(t *testing.T) {
	t.Parallel()

	// No sessions loaded → no workspaces → memory panel skipped.
	session := &stubReplaySessionUsecase{}
	event := &stubReplayEventUsecase{}
	memory := &stubReplayMemoryUsecase{}
	uc := usecase.NewReplayUsecase(session, event, memory)

	bundle, err := uc.Bundle(context.Background(), apptypes.NewReplayCriteriaBuilder(10, 20, 20).Build())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if memory.called {
		t.Fatalf("memory.List must not be called when no sessions have a workspace")
	}
	if len(bundle.Memories()) != 0 {
		t.Fatalf("expected empty memory panel, got %d", len(bundle.Memories()))
	}
}

func TestReplayUsecase_Bundle_SkipsMemoryWhenMemoryLimitZero(t *testing.T) {
	t.Parallel()

	session := &stubReplaySessionUsecase{sessions: []apptypes.SessionSummary{
		sessionSummary(t, "sess-1", "github.com/example/a"),
	}}
	event := &stubReplayEventUsecase{eventsBySession: map[domtypes.SessionID][]*model.Event{}}
	memory := &stubReplayMemoryUsecase{}
	uc := usecase.NewReplayUsecase(session, event, memory)

	// memoryLimit == 0 → memory panel explicitly disabled.
	_, err := uc.Bundle(context.Background(), apptypes.NewReplayCriteriaBuilder(10, 20, 0).Build())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if memory.called {
		t.Fatalf("memory.List must not be called when memoryLimit is 0")
	}
}

func TestReplayUsecase_Bundle_PassesAsOfToMemoryQuery(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	session := &stubReplaySessionUsecase{sessions: []apptypes.SessionSummary{
		sessionSummary(t, "sess-1", "github.com/example/a"),
	}}
	event := &stubReplayEventUsecase{eventsBySession: map[domtypes.SessionID][]*model.Event{}}
	memory := &stubReplayMemoryUsecase{}
	uc := usecase.NewReplayUsecase(session, event, memory)

	_, err := uc.Bundle(context.Background(),
		apptypes.NewReplayCriteriaBuilder(10, 20, 20).MemoryAsOf(asOf).Build())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if got, ok := memory.lastCriteria.AsOf().Value(); !ok || !got.Equal(asOf) {
		t.Fatalf("memory criteria AsOf = %v/ok=%v, want %v", got, ok, asOf)
	}
}
