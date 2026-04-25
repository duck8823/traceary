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

type stubReplaySessionQuery struct {
	sessions []apptypes.SessionSummary
}

func (s *stubReplaySessionQuery) FindLatest(context.Context, domtypes.Client, domtypes.Agent, domtypes.Workspace, bool) (domtypes.Optional[*model.Event], error) {
	return domtypes.None[*model.Event](), nil
}
func (s *stubReplaySessionQuery) ListSummaries(context.Context, int, int, domtypes.SessionID, domtypes.Workspace, domtypes.Client, domtypes.Agent, string, domtypes.Optional[time.Time], domtypes.Optional[time.Time]) ([]apptypes.SessionSummary, error) {
	return s.sessions, nil
}

type stubReplayEventQuery struct {
	eventsBySession map[domtypes.SessionID][]*model.Event
	failureEvents   []*model.Event
	timelineBlocks  []apptypes.TimelineBlock
}

func (s *stubReplayEventQuery) ListRecent(_ context.Context, _, _ int, _ domtypes.EventKind, _ domtypes.Client, _ domtypes.Agent, sessionID domtypes.SessionID, _ domtypes.Workspace, failuresOnly bool, _, _ time.Time, _ string) ([]*model.Event, error) {
	if failuresOnly {
		return s.failureEvents, nil
	}
	return s.eventsBySession[sessionID], nil
}
func (s *stubReplayEventQuery) ListWindow(context.Context, apptypes.EventListCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (s *stubReplayEventQuery) Search(context.Context, string, domtypes.Workspace, domtypes.SessionID, domtypes.Client, domtypes.Agent, domtypes.EventKind, time.Time, time.Time, int, int, bool) ([]*model.Event, error) {
	return nil, nil
}
func (s *stubReplayEventQuery) GetContext(context.Context, domtypes.Workspace, domtypes.SessionID, int) ([]*model.Event, error) {
	return nil, nil
}
func (s *stubReplayEventQuery) GetDetails(context.Context, domtypes.EventID) (apptypes.EventDetails, error) {
	return apptypes.EventDetails{}, nil
}
func (s *stubReplayEventQuery) ListTimelineBlocks(context.Context, domtypes.Workspace, time.Time, time.Time, int, int) ([]apptypes.TimelineBlock, error) {
	return s.timelineBlocks, nil
}

type stubReplayMemoryQuery struct {
	memories     []apptypes.MemorySummary
	lastCriteria apptypes.MemoryListCriteria
	called       bool
}

func (s *stubReplayMemoryQuery) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.called = true
	s.lastCriteria = criteria
	return s.memories, nil
}
func (s *stubReplayMemoryQuery) Search(context.Context, apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}
func (s *stubReplayMemoryQuery) GetDetails(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func sessionSummary(t *testing.T, id string, workspace string) apptypes.SessionSummary {
	t.Helper()
	ws, err := domtypes.WorkspaceFrom(workspace)
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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
	ws, err := domtypes.WorkspaceFrom(workspace)
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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

	session := &stubReplaySessionQuery{sessions: []apptypes.SessionSummary{
		sessionSummary(t, "sess-1", "github.com/example/a"),
		sessionSummary(t, "sess-2", "github.com/example/b"),
	}}
	event := &stubReplayEventQuery{eventsBySession: map[domtypes.SessionID][]*model.Event{}}
	memory := &stubReplayMemoryQuery{memories: []apptypes.MemorySummary{
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

	session := &stubReplaySessionQuery{}
	event := &stubReplayEventQuery{}
	memory := &stubReplayMemoryQuery{}
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

func TestReplayUsecase_Bundle_SkipsMemoryWhenMemoryLimitNonPositive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		memoryLimit int
	}{
		{"zero limit skips panel", 0},
		{"negative limit skips panel", -1},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := &stubReplaySessionQuery{sessions: []apptypes.SessionSummary{
				sessionSummary(t, "sess-1", "github.com/example/a"),
			}}
			event := &stubReplayEventQuery{eventsBySession: map[domtypes.SessionID][]*model.Event{}}
			memory := &stubReplayMemoryQuery{}
			uc := usecase.NewReplayUsecase(session, event, memory)

			if _, err := uc.Bundle(context.Background(), apptypes.NewReplayCriteriaBuilder(10, 20, tc.memoryLimit).Build()); err != nil {
				t.Fatalf("Bundle: %v", err)
			}
			if memory.called {
				t.Fatalf("memory.List must not be called when memoryLimit is %d", tc.memoryLimit)
			}
		})
	}
}

func TestReplayUsecase_Bundle_PassesAsOfToMemoryQuery(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	session := &stubReplaySessionQuery{sessions: []apptypes.SessionSummary{
		sessionSummary(t, "sess-1", "github.com/example/a"),
	}}
	event := &stubReplayEventQuery{eventsBySession: map[domtypes.SessionID][]*model.Event{}}
	memory := &stubReplayMemoryQuery{}
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

func TestReplayUsecase_Bundle_IncludesTimelineBlocksWhenLimitPositive(t *testing.T) {
	t.Parallel()

	blocks := []apptypes.TimelineBlock{
		apptypes.TimelineBlockOf(
			time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 4, 10, 10, 30, 0, 0, time.UTC),
			7,
			[]string{"claude"},
			[]apptypes.TimelineWorkspaceBreakdown{
				apptypes.TimelineWorkspaceBreakdownOf(
					"github.com/example/a", 7,
					[]string{"command_executed"}, []string{"claude"},
					"", apptypes.TimelineSummarySourceKindCounts,
				),
			},
		),
	}
	session := &stubReplaySessionQuery{}
	event := &stubReplayEventQuery{timelineBlocks: blocks}
	uc := usecase.NewReplayUsecase(session, event, nil)

	bundle, err := uc.Bundle(context.Background(),
		apptypes.NewReplayCriteriaBuilder(10, 20, 0).TimelineLimit(5).Build())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	got := bundle.TimelineBlocks()
	if len(got) != 1 {
		t.Fatalf("TimelineBlocks length = %d, want 1", len(got))
	}
	if got[0].EventCount() != 7 {
		t.Fatalf("TimelineBlocks[0] EventCount = %d, want 7", got[0].EventCount())
	}
}

func TestReplayUsecase_Bundle_SkipsTimelineWhenLimitZero(t *testing.T) {
	t.Parallel()

	session := &stubReplaySessionQuery{}
	// Even when ListTimelineBlocks would return data, TimelineLimit=0
	// means the usecase must not call it — fake the stub with non-empty
	// content that must NOT show up in the bundle.
	event := &stubReplayEventQuery{timelineBlocks: []apptypes.TimelineBlock{
		apptypes.TimelineBlockOf(time.Now(), time.Now(), 1, nil, nil),
	}}
	uc := usecase.NewReplayUsecase(session, event, nil)

	bundle, err := uc.Bundle(context.Background(),
		apptypes.NewReplayCriteriaBuilder(10, 20, 0).Build())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if len(bundle.TimelineBlocks()) != 0 {
		t.Fatalf("TimelineBlocks length = %d, want 0 (timeline limit unset)", len(bundle.TimelineBlocks()))
	}
}

func TestReplayUsecase_Bundle_ClustersFailureHotspots(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	workspace := domtypes.Workspace("github.com/example/a")

	newEvent := func(t *testing.T, id, body string, at time.Time) *model.Event {
		t.Helper()
		return model.EventOf(
			domtypes.EventID(id),
			domtypes.EventKindCommandExecuted,
			domtypes.Client("cli"),
			domtypes.Agent("claude"),
			domtypes.SessionID("sess"),
			workspace,
			body,
			at,
		)
	}

	session := &stubReplaySessionQuery{}
	event := &stubReplayEventQuery{failureEvents: []*model.Event{
		newEvent(t, "e1", "go test ./...", now.Add(-time.Hour)),
		newEvent(t, "e2", "go vet ./...", now.Add(-30*time.Minute)),
		newEvent(t, "e3", "npm test", now.Add(-20*time.Minute)),
	}}
	uc := usecase.NewReplayUsecase(session, event, nil)

	bundle, err := uc.Bundle(context.Background(),
		apptypes.NewReplayCriteriaBuilder(10, 20, 0).HotspotLimit(5).Build())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	hotspots := bundle.FailureHotspots()
	if len(hotspots) != 2 {
		t.Fatalf("expected 2 clusters (go, npm), got %d: %+v", len(hotspots), hotspots)
	}
	// Descending count: "go" has 2, "npm" has 1.
	if hotspots[0].Command() != "go" || hotspots[0].Count() != 2 {
		t.Fatalf("first hotspot should be go (count=2), got %+v", hotspots[0])
	}
	if hotspots[1].Command() != "npm" || hotspots[1].Count() != 1 {
		t.Fatalf("second hotspot should be npm (count=1), got %+v", hotspots[1])
	}
	if hotspots[0].Workspace() != workspace.String() {
		t.Fatalf("hotspot workspace = %q, want %q", hotspots[0].Workspace(), workspace.String())
	}
}

func TestReplayUsecase_Bundle_SkipsHotspotsWhenLimitZero(t *testing.T) {
	t.Parallel()

	session := &stubReplaySessionQuery{}
	event := &stubReplayEventQuery{failureEvents: []*model.Event{
		model.EventOf(domtypes.EventID("e1"), domtypes.EventKindCommandExecuted,
			domtypes.Client("cli"), domtypes.Agent("claude"), domtypes.SessionID("s"),
			domtypes.Workspace("w"), "go test", time.Now()),
	}}
	uc := usecase.NewReplayUsecase(session, event, nil)

	bundle, err := uc.Bundle(context.Background(),
		apptypes.NewReplayCriteriaBuilder(10, 20, 0).Build())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if len(bundle.FailureHotspots()) != 0 {
		t.Fatalf("FailureHotspots length = %d, want 0 (hotspot limit unset)", len(bundle.FailureHotspots()))
	}
}
