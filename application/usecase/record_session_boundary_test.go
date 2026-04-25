package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestSessionUsecase_Start(t *testing.T) {
	t.Parallel()

	t.Run("generates and saves session ID on session start", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{}
		sut := usecase.NewSessionUsecase(stub, sessionStub, nil, nil)

		got, err := sut.Start(context.Background(),
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID(""),
			types.Workspace("duck8823/traceary"),
			types.SessionID(""),
		)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if got == nil {
			t.Fatalf("Start() event is nil")
		}
		if !strings.HasPrefix(got.SessionID().String(), "session-") {
			t.Fatalf("SessionID() = %q, want prefix %q", got.SessionID(), "session-")
		}
		if diff := cmp.Diff(types.EventKindSessionStarted, got.Kind()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("session started", got.Body()); diff != "" {
			t.Fatalf("Body() mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestSessionUsecase_StartChild(t *testing.T) {
	t.Parallel()

	parent := model.SessionOf(
		types.SessionID("parent-session"),
		mustTime(t),
		types.None[time.Time](),
		types.Client("hook"),
		types.Agent("claude"),
		types.Workspace("github.com/duck8823/traceary"),
		"", "",
		types.SessionID(""),
	)
	sessionStub := &sessionRepositoryStub{
		session:        parent,
		nextChildOrder: 2,
	}
	sut := usecase.NewSessionUsecase(nil, sessionStub, nil, nil)
	startedAt := time.Date(2026, 4, 25, 10, 11, 12, 0, time.UTC)

	event, err := sut.StartChild(
		context.Background(),
		types.SessionID("parent-session"),
		types.SessionID("parent-session:sub:toolu_1"),
		types.Agent("claude/code-reviewer"),
		types.Workspace("github.com/duck8823/traceary"),
		types.EventID("toolu_1"),
		"task",
		startedAt,
	)
	if err != nil {
		t.Fatalf("StartChild() error = %v", err)
	}
	if event == nil {
		t.Fatalf("StartChild() event is nil")
	}
	if diff := cmp.Diff(types.SessionID("parent-session:sub:toolu_1"), event.SessionID()); diff != "" {
		t.Fatalf("event SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(startedAt, event.CreatedAt()); diff != "" {
		t.Fatalf("event CreatedAt() mismatch (-want +got):\n%s", diff)
	}
	child := sessionStub.savedBoundary
	if child == nil {
		t.Fatalf("saved child session is nil")
	}
	if diff := cmp.Diff(types.SessionID("parent-session"), child.ParentSessionID()); diff != "" {
		t.Fatalf("ParentSessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.EventID("toolu_1"), child.SpawnEventID()); diff != "" {
		t.Fatalf("SpawnEventID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("task", child.SubagentKind()); diff != "" {
		t.Fatalf("SubagentKind() mismatch (-want +got):\n%s", diff)
	}
	if got, ok := child.SpawnOrder().Value(); !ok {
		t.Fatalf("SpawnOrder() should be present")
	} else if diff := cmp.Diff(2, got); diff != "" {
		t.Fatalf("SpawnOrder() mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionUsecase_End(t *testing.T) {
	t.Parallel()

	t.Run("session end returns error when session ID is missing", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{}
		sut := usecase.NewSessionUsecase(stub, sessionStub, nil, nil)

		_, err := sut.End(context.Background(),
			types.Client(""),
			types.Agent("codex"),
			types.SessionID(""),
			types.Workspace(""),
			"",
		)
		if err == nil {
			t.Fatalf("End() error = nil, want error")
		}
	})

	t.Run("saves session end event", func(t *testing.T) {
		t.Parallel()

		sessionID, _ := types.SessionIDFrom("session-1")
		agent, _ := types.AgentFrom("codex")
		existing := model.SessionOf(
			sessionID, mustTime(t), types.None[time.Time](),
			types.Client("cli"), agent, types.Workspace("duck8823/traceary"),
			"", "", types.SessionID(""),
		)
		stub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{session: existing}
		sut := usecase.NewSessionUsecase(stub, sessionStub, nil, nil)

		got, err := sut.End(context.Background(),
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			"",
		)
		if err != nil {
			t.Fatalf("End() error = %v", err)
		}
		if diff := cmp.Diff(types.EventKindSessionEnded, got.Kind()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("session-1", got.SessionID().String()); diff != "" {
			t.Fatalf("SessionID() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("session ended", got.Body()); diff != "" {
			t.Fatalf("Body() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("session end inherits client/agent/repo from start", func(t *testing.T) {
		t.Parallel()

		sessionID, err := types.SessionIDFrom("session-1")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		startAgent, err := types.AgentFrom("claude")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}

		stub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{
			session: model.SessionOf(
				sessionID,
				mustTime(t),
				types.None[time.Time](),
				types.Client("hook"),
				startAgent,
				types.Workspace("repo-from-start"),
				"", "", types.SessionID(""),
			),
		}
		sut := usecase.NewSessionUsecase(stub, sessionStub, nil, nil)

		got, err := sut.End(context.Background(),
			types.Client(""),
			types.Agent(""),
			types.SessionID("session-1"),
			types.Workspace(""),
			"",
		)
		if err != nil {
			t.Fatalf("End() error = %v", err)
		}
		if diff := cmp.Diff(types.Client("hook"), got.Client()); diff != "" {
			t.Fatalf("Client() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("claude", got.Agent().String()); diff != "" {
			t.Fatalf("Agent() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Workspace("repo-from-start"), got.Workspace()); diff != "" {
			t.Fatalf("Workspace() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("session end prefers explicit client/agent over inherited", func(t *testing.T) {
		t.Parallel()

		sessionID, err := types.SessionIDFrom("session-1")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		startAgent, err := types.AgentFrom("claude")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}

		stub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{
			session: model.SessionOf(
				sessionID,
				mustTime(t),
				types.None[time.Time](),
				types.Client("hook"),
				startAgent,
				types.Workspace("repo-from-start"),
				"", "", types.SessionID(""),
			),
		}
		sut := usecase.NewSessionUsecase(stub, sessionStub, nil, nil)

		got, err := sut.End(context.Background(),
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace("repo-explicit"),
			"",
		)
		if err != nil {
			t.Fatalf("End() error = %v", err)
		}
		if diff := cmp.Diff(types.Client("cli"), got.Client()); diff != "" {
			t.Fatalf("Client() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("codex", got.Agent().String()); diff != "" {
			t.Fatalf("Agent() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Workspace("repo-explicit"), got.Workspace()); diff != "" {
			t.Fatalf("Workspace() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("propagates error from session lookup", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{findErr: errors.New("boom")}
		sut := usecase.NewSessionUsecase(stub, sessionStub, nil, nil)

		_, err := sut.End(context.Background(),
			types.Client(""),
			types.Agent("manual"),
			types.SessionID("session-1"),
			types.Workspace(""),
			"",
		)
		if err == nil {
			t.Fatalf("End() error = nil, want error")
		}
	})

	t.Run("returns ErrInvalidSessionState when session is already ended", func(t *testing.T) {
		t.Parallel()

		sessionID, err := types.SessionIDFrom("session-already-ended")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		agent, err := types.AgentFrom("claude")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}
		endedAt := mustTime(t).Add(time.Hour)
		alreadyEnded := model.SessionOf(
			sessionID, mustTime(t), types.Some(endedAt),
			types.Client("cli"), agent, types.Workspace("duck8823/traceary"),
			"", "first end", types.SessionID(""),
		)

		stub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{session: alreadyEnded}
		sut := usecase.NewSessionUsecase(stub, sessionStub, nil, nil)

		_, err = sut.End(context.Background(),
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID("session-already-ended"),
			types.Workspace("duck8823/traceary"),
			"second end attempt",
		)
		if err == nil {
			t.Fatalf("End() error = nil, want ErrInvalidSessionState")
		}
		if !errors.Is(err, model.ErrInvalidSessionState) {
			t.Fatalf("End() error = %v, want ErrInvalidSessionState", err)
		}
		if sessionStub.saveBoundaryCalled {
			t.Fatalf("SessionRepository.SaveBoundary() should not be called when session is already ended")
		}
	})
}

type sessionRepositoryStub struct {
	session            *model.Session
	empty              bool
	findErr            error
	saveCalled         bool
	saved              *model.Session
	saveErr            error
	saveBoundaryCalled bool
	savedBoundary      *model.Session
	savedEvent         *model.Event
	saveBoundaryErr    error
	nextChildOrder     int
	nextChildOrderErr  error
}

func (s *sessionRepositoryStub) FindByID(
	_ context.Context,
	sessionID types.SessionID,
) (types.Optional[*model.Session], error) {
	if s.findErr != nil {
		return types.None[*model.Session](), s.findErr
	}
	if s.empty || s.session == nil {
		return types.None[*model.Session](), nil
	}
	if s.session.SessionID() != sessionID {
		return types.None[*model.Session](), nil
	}
	return types.Some(s.session), nil
}

func (s *sessionRepositoryStub) Save(_ context.Context, session *model.Session) error {
	s.saveCalled = true
	s.saved = session
	return s.saveErr
}

func (s *sessionRepositoryStub) SaveBoundary(_ context.Context, session *model.Session, event *model.Event) error {
	s.saveBoundaryCalled = true
	s.savedBoundary = session
	s.savedEvent = event
	return s.saveBoundaryErr
}

func (s *sessionRepositoryStub) NextChildSpawnOrder(_ context.Context, _ types.SessionID) (int, error) {
	if s.nextChildOrderErr != nil {
		return 0, s.nextChildOrderErr
	}
	if s.nextChildOrder == 0 {
		return 1, nil
	}
	return s.nextChildOrder, nil
}

func mustTime(t *testing.T) time.Time {
	t.Helper()

	return time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)
}

func TestSessionUsecase_SessionSaver(t *testing.T) {
	t.Parallel()

	t.Run("calls SaveBoundary on session start", func(t *testing.T) {
		t.Parallel()

		eventStub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{}
		sut := usecase.NewSessionUsecase(eventStub, sessionStub, nil, nil)

		_, err := sut.Start(context.Background(),
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID(""),
			types.Workspace("duck8823/traceary"),
			types.SessionID(""),
		)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if !sessionStub.saveBoundaryCalled {
			t.Fatalf("SessionRepository.SaveBoundary() was not called")
		}
		if _, ok := sessionStub.savedBoundary.EndedAt().Value(); ok {
			t.Fatalf("session.EndedAt() should be empty for start")
		}
		if sessionStub.savedEvent.Kind() != types.EventKindSessionStarted {
			t.Fatalf("event Kind() = %v, want session_started", sessionStub.savedEvent.Kind())
		}
	})

	t.Run("calls SaveBoundary on session end with existing session", func(t *testing.T) {
		t.Parallel()

		sessionID, err := types.SessionIDFrom("test-session")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		agent, err := types.AgentFrom("claude")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}

		eventStub := &eventRepositoryStub{}
		existingSession := model.SessionOf(
			sessionID,
			mustTime(t),
			types.None[time.Time](),
			types.Client("cli"),
			agent,
			types.Workspace("duck8823/traceary"),
			"", "", types.SessionID(""),
		)
		sessionStub := &sessionRepositoryStub{session: existingSession}
		sut := usecase.NewSessionUsecase(eventStub, sessionStub, nil, nil)

		_, err = sut.End(context.Background(),
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID("test-session"),
			types.Workspace("duck8823/traceary"),
			"test summary",
		)
		if err != nil {
			t.Fatalf("End() error = %v", err)
		}
		if !sessionStub.saveBoundaryCalled {
			t.Fatalf("SessionRepository.SaveBoundary() was not called")
		}
		if _, ok := sessionStub.savedBoundary.EndedAt().Value(); !ok {
			t.Fatalf("session.EndedAt() should be present for end")
		}
		if diff := cmp.Diff("test summary", sessionStub.savedBoundary.Summary()); diff != "" {
			t.Fatalf("Summary() mismatch (-want +got):\n%s", diff)
		}
		if sessionStub.savedEvent.Kind() != types.EventKindSessionEnded {
			t.Fatalf("event Kind() = %v, want session_ended", sessionStub.savedEvent.Kind())
		}
	})

	t.Run("session end returns ErrInvalidSessionState when session not found", func(t *testing.T) {
		t.Parallel()

		eventStub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{empty: true}
		sut := usecase.NewSessionUsecase(eventStub, sessionStub, nil, nil)

		_, err := sut.End(context.Background(),
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID("missing-session"),
			types.Workspace("duck8823/traceary"),
			"",
		)
		if err == nil {
			t.Fatalf("End() error = nil, want ErrInvalidSessionState")
		}
		if !errors.Is(err, model.ErrInvalidSessionState) {
			t.Fatalf("End() error = %v, want ErrInvalidSessionState", err)
		}
		if sessionStub.saveBoundaryCalled {
			t.Fatalf("SessionRepository.SaveBoundary() should not be called when session is not found")
		}
	})

	t.Run("session start returns ErrInvalidSessionState when explicit session ID already exists", func(t *testing.T) {
		t.Parallel()

		existingID, _ := types.SessionIDFrom("existing-session")
		agent, _ := types.AgentFrom("claude")
		existingSession := model.SessionOf(
			existingID, mustTime(t), types.None[time.Time](),
			types.Client("cli"), agent, types.Workspace("duck8823/traceary"),
			"", "", types.SessionID(""),
		)
		eventStub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{session: existingSession}
		sut := usecase.NewSessionUsecase(eventStub, sessionStub, nil, nil)

		_, err := sut.Start(context.Background(),
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID("existing-session"),
			types.Workspace("duck8823/traceary"),
			types.SessionID(""),
		)
		if err == nil {
			t.Fatalf("Start() error = nil, want ErrInvalidSessionState")
		}
		if !errors.Is(err, model.ErrInvalidSessionState) {
			t.Fatalf("Start() error = %v, want ErrInvalidSessionState", err)
		}
		if sessionStub.saveBoundaryCalled {
			t.Fatalf("SessionRepository.SaveBoundary() should not be called when session already exists")
		}
	})

	t.Run("returns error when SaveBoundary fails", func(t *testing.T) {
		t.Parallel()

		eventStub := &eventRepositoryStub{}
		sessionStub := &sessionRepositoryStub{saveBoundaryErr: errors.New("save failed")}
		sut := usecase.NewSessionUsecase(eventStub, sessionStub, nil, nil)

		_, err := sut.Start(context.Background(),
			types.Client("cli"),
			types.Agent("claude"),
			types.SessionID(""),
			types.Workspace("duck8823/traceary"),
			types.SessionID(""),
		)
		if err == nil {
			t.Fatalf("Start() error = nil, want error")
		}
	})
}
