package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type sessionQueryServiceStub struct {
	findLatestResult types.Optional[*model.Event]
	findLatestErr    error
	findLatestCalls  int
	findLatestClient types.Client
	findLatestAgent  types.Agent
	findLatestWS     types.Workspace
	findLatestActive bool

	listSummariesResult []apptypes.SessionSummary
	listSummariesErr    error
	listSummariesCalls  int
	listLimit           int
	listOffset          int
	listSessionID       types.SessionID
	listWorkspace       types.Workspace
	listClient          types.Client
	listAgent           types.Agent
	listLabel           string
	listFrom            types.Optional[time.Time]
	listTo              types.Optional[time.Time]
}

func (s *sessionQueryServiceStub) FindLatest(
	_ context.Context,
	client types.Client,
	agent types.Agent,
	workspace types.Workspace,
	activeOnly bool,
) (types.Optional[*model.Event], error) {
	s.findLatestCalls++
	s.findLatestClient = client
	s.findLatestAgent = agent
	s.findLatestWS = workspace
	s.findLatestActive = activeOnly
	if s.findLatestErr != nil {
		return types.Empty[*model.Event](), s.findLatestErr
	}
	return s.findLatestResult, nil
}

func (s *sessionQueryServiceStub) ListSummaries(
	_ context.Context,
	limit, offset int,
	sessionID types.SessionID,
	workspace types.Workspace,
	client types.Client,
	agent types.Agent,
	label string,
	from, to types.Optional[time.Time],
) ([]apptypes.SessionSummary, error) {
	s.listSummariesCalls++
	s.listLimit = limit
	s.listOffset = offset
	s.listSessionID = sessionID
	s.listWorkspace = workspace
	s.listClient = client
	s.listAgent = agent
	s.listLabel = label
	s.listFrom = from
	s.listTo = to
	if s.listSummariesErr != nil {
		return nil, s.listSummariesErr
	}
	return s.listSummariesResult, nil
}

type eventQueryServiceStub struct {
	listRecentResult          []*model.Event
	listRecentResultByKind    map[types.EventKind][]*model.Event
	listRecentErr             error
	listRecentErrByKind       map[types.EventKind]error
	listRecentCalls           int
	listRecentCallsByKind     map[types.EventKind]int
	listRecentLimit           int
	listRecentLimitByKind     map[types.EventKind]int
	listRecentWorkspace       types.Workspace
	listRecentWorkspaceByKind map[types.EventKind]types.Workspace
}

func (s *eventQueryServiceStub) ListRecent(
	_ context.Context,
	limit, _ int,
	kind types.EventKind,
	_ types.Client,
	_ types.Agent,
	_ types.SessionID,
	workspace types.Workspace,
	_ bool,
	_, _ time.Time,
) ([]*model.Event, error) {
	s.listRecentCalls++
	s.listRecentLimit = limit
	s.listRecentWorkspace = workspace
	if s.listRecentCallsByKind == nil {
		s.listRecentCallsByKind = make(map[types.EventKind]int)
	}
	if s.listRecentLimitByKind == nil {
		s.listRecentLimitByKind = make(map[types.EventKind]int)
	}
	if s.listRecentWorkspaceByKind == nil {
		s.listRecentWorkspaceByKind = make(map[types.EventKind]types.Workspace)
	}
	s.listRecentCallsByKind[kind]++
	s.listRecentLimitByKind[kind] = limit
	s.listRecentWorkspaceByKind[kind] = workspace

	if err, ok := s.listRecentErrByKind[kind]; ok && err != nil {
		return nil, err
	}
	if s.listRecentErr != nil {
		return nil, s.listRecentErr
	}
	if result, ok := s.listRecentResultByKind[kind]; ok {
		return result, nil
	}
	return s.listRecentResult, nil
}

func (s *eventQueryServiceStub) Search(
	_ context.Context,
	_ string,
	_ types.Workspace,
	_ types.SessionID,
	_ types.Client,
	_ types.Agent,
	_ types.EventKind,
	_, _ time.Time,
	_, _ int,
	_ bool,
) ([]*model.Event, error) {
	return nil, nil
}

func (s *eventQueryServiceStub) GetContext(
	_ context.Context,
	_ types.Workspace,
	_ types.SessionID,
	_ int,
) ([]*model.Event, error) {
	return nil, nil
}

func (s *eventQueryServiceStub) GetDetails(
	_ context.Context,
	_ types.EventID,
) (apptypes.EventDetails, error) {
	return apptypes.EventDetails{}, nil
}

func (s *eventQueryServiceStub) ListWindow(
	_ context.Context,
	_ apptypes.EventListCriteria,
) ([]*model.Event, error) {
	return nil, nil
}

func (s *eventQueryServiceStub) ListTimelineBlocks(
	_ context.Context,
	_ types.Workspace,
	_, _ time.Time,
	_, _ int,
) ([]apptypes.TimelineBlock, error) {
	return nil, nil
}

func TestSessionUsecase_Label(t *testing.T) {
	t.Parallel()

	sessionID, err := types.SessionIDOf("session-1")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("claude")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}

	t.Run("sets label on existing session", func(t *testing.T) {
		t.Parallel()

		existing := model.SessionOf(
			sessionID,
			mustTime(t),
			types.Empty[time.Time](),
			types.Client("cli"),
			agent,
			types.Workspace("duck8823/traceary"),
			"", "", types.SessionID(""),
		)
		sessionStub := &sessionRepositoryStub{session: existing}
		sut := usecase.NewSessionUsecase(nil, sessionStub, nil, nil)

		if err := sut.Label(context.Background(), types.SessionID("session-1"), "bugfix"); err != nil {
			t.Fatalf("Label() error = %v", err)
		}
		if !sessionStub.saveCalled {
			t.Fatalf("SessionRepository.Save() was not called")
		}
		if diff := cmp.Diff("bugfix", sessionStub.saved.Label()); diff != "" {
			t.Fatalf("Label() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns error when session ID is empty", func(t *testing.T) {
		t.Parallel()

		sessionStub := &sessionRepositoryStub{}
		sut := usecase.NewSessionUsecase(nil, sessionStub, nil, nil)

		err := sut.Label(context.Background(), types.SessionID("   "), "bugfix")
		if err == nil {
			t.Fatalf("Label() error = nil, want error")
		}
		if sessionStub.saveCalled {
			t.Fatalf("SessionRepository.Save() should not be called when session ID is empty")
		}
	})

	t.Run("returns error when session is not found", func(t *testing.T) {
		t.Parallel()

		sessionStub := &sessionRepositoryStub{empty: true}
		sut := usecase.NewSessionUsecase(nil, sessionStub, nil, nil)

		err := sut.Label(context.Background(), types.SessionID("session-1"), "bugfix")
		if err == nil {
			t.Fatalf("Label() error = nil, want error")
		}
		if sessionStub.saveCalled {
			t.Fatalf("SessionRepository.Save() should not be called when session is not found")
		}
	})

	t.Run("propagates FindByID error", func(t *testing.T) {
		t.Parallel()

		sessionStub := &sessionRepositoryStub{findErr: errors.New("find failed")}
		sut := usecase.NewSessionUsecase(nil, sessionStub, nil, nil)

		err := sut.Label(context.Background(), types.SessionID("session-1"), "bugfix")
		if err == nil {
			t.Fatalf("Label() error = nil, want error")
		}
	})

	t.Run("propagates Save error", func(t *testing.T) {
		t.Parallel()

		existing := model.SessionOf(
			sessionID,
			mustTime(t),
			types.Empty[time.Time](),
			types.Client("cli"),
			agent,
			types.Workspace("duck8823/traceary"),
			"", "", types.SessionID(""),
		)
		sessionStub := &sessionRepositoryStub{session: existing, saveErr: errors.New("save failed")}
		sut := usecase.NewSessionUsecase(nil, sessionStub, nil, nil)

		err := sut.Label(context.Background(), types.SessionID("session-1"), "bugfix")
		if err == nil {
			t.Fatalf("Label() error = nil, want error")
		}
	})
}

func TestSessionUsecase_Active(t *testing.T) {
	t.Parallel()

	t.Run("delegates to FindLatest with activeOnly=true", func(t *testing.T) {
		t.Parallel()

		event, err := model.NewEvent(
			types.EventID("event-1"),
			types.EventKindSessionStarted,
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			"session started",
		)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		queryStub := &sessionQueryServiceStub{findLatestResult: types.Of(event)}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		criteria := apptypes.NewSessionLookupCriteriaBuilder().
			Client(types.Client("cli")).
			Agent(types.Agent("claude")).
			Workspace(types.Workspace("duck8823/traceary")).
			Build()

		got, err := sut.Active(context.Background(), criteria)
		if err != nil {
			t.Fatalf("Active() error = %v", err)
		}
		if !got.IsPresent() {
			t.Fatalf("Active() result is empty, want present")
		}
		if diff := cmp.Diff(1, queryStub.findLatestCalls); diff != "" {
			t.Fatalf("findLatestCalls mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(true, queryStub.findLatestActive); diff != "" {
			t.Fatalf("findLatestActive mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Client("cli"), queryStub.findLatestClient); diff != "" {
			t.Fatalf("findLatestClient mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Agent("claude"), queryStub.findLatestAgent); diff != "" {
			t.Fatalf("findLatestAgent mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Workspace("duck8823/traceary"), queryStub.findLatestWS); diff != "" {
			t.Fatalf("findLatestWS mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns empty Optional when query returns empty", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{findLatestResult: types.Empty[*model.Event]()}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		got, err := sut.Active(context.Background(), apptypes.NewSessionLookupCriteriaBuilder().Build())
		if err != nil {
			t.Fatalf("Active() error = %v", err)
		}
		if got.IsPresent() {
			t.Fatalf("Active() result is present, want empty")
		}
	})

	t.Run("propagates query error", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{findLatestErr: errors.New("query failed")}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		_, err := sut.Active(context.Background(), apptypes.NewSessionLookupCriteriaBuilder().Build())
		if err == nil {
			t.Fatalf("Active() error = nil, want error")
		}
	})
}

func TestSessionUsecase_Latest(t *testing.T) {
	t.Parallel()

	t.Run("delegates to FindLatest with activeOnly=false", func(t *testing.T) {
		t.Parallel()

		event, err := model.NewEvent(
			types.EventID("event-1"),
			types.EventKindSessionStarted,
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			"session started",
		)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		queryStub := &sessionQueryServiceStub{findLatestResult: types.Of(event)}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		criteria := apptypes.NewSessionLookupCriteriaBuilder().
			Workspace(types.Workspace("duck8823/traceary")).
			Build()

		got, err := sut.Latest(context.Background(), criteria)
		if err != nil {
			t.Fatalf("Latest() error = %v", err)
		}
		if !got.IsPresent() {
			t.Fatalf("Latest() result is empty, want present")
		}
		if diff := cmp.Diff(1, queryStub.findLatestCalls); diff != "" {
			t.Fatalf("findLatestCalls mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(false, queryStub.findLatestActive); diff != "" {
			t.Fatalf("findLatestActive mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns empty Optional when query returns empty", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{findLatestResult: types.Empty[*model.Event]()}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		got, err := sut.Latest(context.Background(), apptypes.NewSessionLookupCriteriaBuilder().Build())
		if err != nil {
			t.Fatalf("Latest() error = %v", err)
		}
		if got.IsPresent() {
			t.Fatalf("Latest() result is present, want empty")
		}
	})

	t.Run("propagates query error", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{findLatestErr: errors.New("query failed")}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		_, err := sut.Latest(context.Background(), apptypes.NewSessionLookupCriteriaBuilder().Build())
		if err == nil {
			t.Fatalf("Latest() error = nil, want error")
		}
	})
}

func TestSessionUsecase_List(t *testing.T) {
	t.Parallel()

	t.Run("returns error when limit is zero", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		criteria := apptypes.NewSessionListCriteriaBuilder(0).Build()
		_, err := sut.List(context.Background(), criteria)
		if err == nil {
			t.Fatalf("List() error = nil, want error")
		}
		if queryStub.listSummariesCalls != 0 {
			t.Fatalf("ListSummaries should not be called on validation failure")
		}
	})

	t.Run("returns error when limit is negative", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		criteria := apptypes.NewSessionListCriteriaBuilder(-1).Build()
		_, err := sut.List(context.Background(), criteria)
		if err == nil {
			t.Fatalf("List() error = nil, want error")
		}
	})

	t.Run("returns error when offset is negative", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		criteria := apptypes.NewSessionListCriteriaBuilder(10).Offset(-1).Build()
		_, err := sut.List(context.Background(), criteria)
		if err == nil {
			t.Fatalf("List() error = nil, want error")
		}
		if queryStub.listSummariesCalls != 0 {
			t.Fatalf("ListSummaries should not be called on validation failure")
		}
	})

	t.Run("returns summaries on happy path", func(t *testing.T) {
		t.Parallel()

		want := []apptypes.SessionSummary{
			apptypes.SessionSummaryOf(
				types.SessionID("session-1"),
				types.Workspace("duck8823/traceary"),
				mustTime(t),
				types.Empty[time.Time](),
				"active",
				10,
				5,
				[]string{"claude"},
				"label-1",
				"",
				types.SessionID(""),
			),
		}
		queryStub := &sessionQueryServiceStub{listSummariesResult: want}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		criteria := apptypes.NewSessionListCriteriaBuilder(20).
			Offset(5).
			Workspace(types.Workspace("duck8823/traceary")).
			Agent(types.Agent("claude")).
			Label("label-1").
			Build()

		got, err := sut.List(context.Background(), criteria)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if diff := cmp.Diff(len(want), len(got)); diff != "" {
			t.Fatalf("List() length mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want[0].SessionID(), got[0].SessionID()); diff != "" {
			t.Fatalf("List()[0].SessionID mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want[0].Workspace(), got[0].Workspace()); diff != "" {
			t.Fatalf("List()[0].Workspace mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(20, queryStub.listLimit); diff != "" {
			t.Fatalf("limit mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(5, queryStub.listOffset); diff != "" {
			t.Fatalf("offset mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Workspace("duck8823/traceary"), queryStub.listWorkspace); diff != "" {
			t.Fatalf("workspace mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Agent("claude"), queryStub.listAgent); diff != "" {
			t.Fatalf("agent mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("label-1", queryStub.listLabel); diff != "" {
			t.Fatalf("label mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("propagates query error", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{listSummariesErr: errors.New("query failed")}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		criteria := apptypes.NewSessionListCriteriaBuilder(10).Build()
		_, err := sut.List(context.Background(), criteria)
		if err == nil {
			t.Fatalf("List() error = nil, want error")
		}
	})
}

func TestSessionUsecase_Tree(t *testing.T) {
	t.Parallel()

	t.Run("returns error when limit is zero", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		_, err := sut.Tree(context.Background(), types.Workspace("duck8823/traceary"), 0)
		if err == nil {
			t.Fatalf("Tree() error = nil, want error")
		}
		if queryStub.listSummariesCalls != 0 {
			t.Fatalf("ListSummaries should not be called on validation failure")
		}
	})

	t.Run("returns error when limit is negative", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		_, err := sut.Tree(context.Background(), types.Workspace("duck8823/traceary"), -1)
		if err == nil {
			t.Fatalf("Tree() error = nil, want error")
		}
	})

	t.Run("returns summaries filtered by workspace", func(t *testing.T) {
		t.Parallel()

		want := []apptypes.SessionSummary{
			apptypes.SessionSummaryOf(
				types.SessionID("session-1"),
				types.Workspace("duck8823/traceary"),
				mustTime(t),
				types.Empty[time.Time](),
				"active",
				10,
				5,
				[]string{"claude"},
				"",
				"",
				types.SessionID(""),
			),
		}
		queryStub := &sessionQueryServiceStub{listSummariesResult: want}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		got, err := sut.Tree(context.Background(), types.Workspace("duck8823/traceary"), 50)
		if err != nil {
			t.Fatalf("Tree() error = %v", err)
		}
		if diff := cmp.Diff(len(want), len(got)); diff != "" {
			t.Fatalf("Tree() length mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want[0].SessionID(), got[0].SessionID()); diff != "" {
			t.Fatalf("Tree()[0].SessionID mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want[0].Workspace(), got[0].Workspace()); diff != "" {
			t.Fatalf("Tree()[0].Workspace mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(50, queryStub.listLimit); diff != "" {
			t.Fatalf("limit mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(0, queryStub.listOffset); diff != "" {
			t.Fatalf("offset mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Workspace("duck8823/traceary"), queryStub.listWorkspace); diff != "" {
			t.Fatalf("workspace mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("propagates query error", func(t *testing.T) {
		t.Parallel()

		queryStub := &sessionQueryServiceStub{listSummariesErr: errors.New("query failed")}
		sut := usecase.NewSessionUsecase(nil, nil, queryStub, nil)

		_, err := sut.Tree(context.Background(), types.Workspace("duck8823/traceary"), 10)
		if err == nil {
			t.Fatalf("Tree() error = nil, want error")
		}
	})
}

func TestSessionUsecase_Handoff(t *testing.T) {
	t.Parallel()

	t.Run("returns empty Optional when no session matches", func(t *testing.T) {
		t.Parallel()

		sessionQueryStub := &sessionQueryServiceStub{listSummariesResult: nil}
		eventQueryStub := &eventQueryServiceStub{}
		sut := usecase.NewSessionUsecase(nil, nil, sessionQueryStub, eventQueryStub)

		got, err := sut.Handoff(context.Background(), types.SessionID("missing"), types.Workspace(""), 5)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if got.IsPresent() {
			t.Fatalf("Handoff() result is present, want empty")
		}
		if eventQueryStub.listRecentCalls != 0 {
			t.Fatalf("ListRecent should not be called when no session matches")
		}
	})

	t.Run("builds handoff summary from session metadata and recent commands", func(t *testing.T) {
		t.Parallel()

		summary := apptypes.SessionSummaryOf(
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			mustTime(t),
			types.Of(mustTime(t).Add(time.Hour)),
			"ended",
			42,
			30,
			[]string{"claude", "codex"},
			"docs",
			"Wrapped up documentation task.",
			types.SessionID(""),
		)
		cmdEvent1, err := model.NewEvent(
			types.EventID("event-1"),
			types.EventKindCommandExecuted,
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			"go test ./...",
		)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		cmdEvent2, err := model.NewEvent(
			types.EventID("event-2"),
			types.EventKindCommandExecuted,
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			"this command exceeds sixty runes so that the truncation logic in Handoff kicks in",
		)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}

		sessionQueryStub := &sessionQueryServiceStub{
			listSummariesResult: []apptypes.SessionSummary{summary},
		}
		eventQueryStub := &eventQueryServiceStub{
			listRecentResult: []*model.Event{cmdEvent1, cmdEvent2},
		}
		sut := usecase.NewSessionUsecase(nil, nil, sessionQueryStub, eventQueryStub)

		got, err := sut.Handoff(
			context.Background(),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			5,
		)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if !got.IsPresent() {
			t.Fatalf("Handoff() result is empty, want present")
		}

		result, _ := got.Get()
		if diff := cmp.Diff(types.SessionID("session-1"), result.SessionID()); diff != "" {
			t.Fatalf("SessionID() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Workspace("duck8823/traceary"), result.Workspace()); diff != "" {
			t.Fatalf("Workspace() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("docs", result.Label()); diff != "" {
			t.Fatalf("Label() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("ended", result.Status()); diff != "" {
			t.Fatalf("Status() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(42, result.TotalEvents()); diff != "" {
			t.Fatalf("TotalEvents() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(30, result.CommandCount()); diff != "" {
			t.Fatalf("CommandCount() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"claude", "codex"}, result.Agents()); diff != "" {
			t.Fatalf("Agents() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("Wrapped up documentation task.", result.Summary()); diff != "" {
			t.Fatalf("Summary() mismatch (-want +got):\n%s", diff)
		}

		wantCommands := []string{
			"go test ./...",
			"this command exceeds sixty runes so that the truncation logi\u2026",
		}
		if diff := cmp.Diff(wantCommands, result.RecentCommands()); diff != "" {
			t.Fatalf("RecentCommands() mismatch (-want +got):\n%s", diff)
		}

		if diff := cmp.Diff(5, eventQueryStub.listRecentLimitByKind[types.EventKindCommandExecuted]); diff != "" {
			t.Fatalf("command listRecentLimit mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(1, eventQueryStub.listRecentLimitByKind[types.EventKindCompactSummary]); diff != "" {
			t.Fatalf("compact summary listRecentLimit mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("propagates ListSummaries error", func(t *testing.T) {
		t.Parallel()

		sessionQueryStub := &sessionQueryServiceStub{listSummariesErr: errors.New("list failed")}
		eventQueryStub := &eventQueryServiceStub{}
		sut := usecase.NewSessionUsecase(nil, nil, sessionQueryStub, eventQueryStub)

		_, err := sut.Handoff(context.Background(), types.SessionID("session-1"), types.Workspace(""), 5)
		if err == nil {
			t.Fatalf("Handoff() error = nil, want error")
		}
	})

	t.Run("propagates ListRecent error", func(t *testing.T) {
		t.Parallel()

		summary := apptypes.SessionSummaryOf(
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			mustTime(t),
			types.Empty[time.Time](),
			"active",
			0,
			0,
			nil,
			"",
			"",
			types.SessionID(""),
		)
		sessionQueryStub := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{summary}}
		eventQueryStub := &eventQueryServiceStub{listRecentErr: errors.New("recent failed")}
		sut := usecase.NewSessionUsecase(nil, nil, sessionQueryStub, eventQueryStub)

		_, err := sut.Handoff(context.Background(), types.SessionID("session-1"), types.Workspace(""), 5)
		if err == nil {
			t.Fatalf("Handoff() error = nil, want error")
		}
	})
}
