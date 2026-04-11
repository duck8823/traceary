package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestRecordSessionBoundaryUsecase_Run(t *testing.T) {
	t.Parallel()

	t.Run("session start で session ID を生成して保存できる", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub, nil)

		got, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			Client: "cli",
			Agent:  "codex",
			Repo:   "duck8823/traceary",
			Kind:   types.EventKindSessionStarted,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got == nil {
			t.Fatalf("Run() event is nil")
		}
		if !strings.HasPrefix(got.SessionID().String(), "session-") {
			t.Fatalf("SessionID() = %q, want prefix %q", got.SessionID(), "session-")
		}
		if got.Kind() != types.EventKindSessionStarted {
			t.Fatalf("Kind() = %q, want %q", got.Kind(), types.EventKindSessionStarted)
		}
		if got.Body() != "session started" {
			t.Fatalf("Body() = %q, want %q", got.Body(), "session started")
		}
	})

	t.Run("session end は session ID 未指定だとエラー", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub, nil)

		_, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			Agent:  "codex",
			Kind:   types.EventKindSessionEnded,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("session end を保存できる", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub, nil)

		got, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			Client:    "cli",
			Agent:     "codex",
			SessionID: "session-1",
			Repo:      "duck8823/traceary",
			Kind:      types.EventKindSessionEnded,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got.Kind() != types.EventKindSessionEnded {
			t.Fatalf("Kind() = %q, want %q", got.Kind(), types.EventKindSessionEnded)
		}
		if got.SessionID().String() != "session-1" {
			t.Fatalf("SessionID() = %q, want %q", got.SessionID(), "session-1")
		}
		if got.Body() != "session ended" {
			t.Fatalf("Body() = %q, want %q", got.Body(), "session ended")
		}
	})

	t.Run("session end は開始時の client/agent/repo を引き継げる", func(t *testing.T) {
		t.Parallel()

		sessionID, err := types.SessionIDOf("session-1")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}
		startAgent, err := types.AgentOf("claude")
		if err != nil {
			t.Fatalf("AgentOf() error = %v", err)
		}

		stub := &eventRepositoryStub{}
		finder := &sessionStartedEventFinderStub{
			event: model.EventOf(
				mustEventID(t, "event-started"),
				types.EventKindSessionStarted,
				"hook",
				startAgent,
				sessionID,
				"repo-from-start",
				"session started",
				mustTime(t),
			),
		}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub, finder)

		got, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			DefaultClient: "cli",
			DefaultAgent:  "manual",
			SessionID:     "session-1",
			Kind:          types.EventKindSessionEnded,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got.Client() != "hook" {
			t.Fatalf("Client() = %q, want %q", got.Client(), "hook")
		}
		if got.Agent().String() != "claude" {
			t.Fatalf("Agent() = %q, want %q", got.Agent(), "claude")
		}
		if got.Repo() != "repo-from-start" {
			t.Fatalf("Repo() = %q, want %q", got.Repo(), "repo-from-start")
		}
	})

	t.Run("session end は明示的な client/agent を優先する", func(t *testing.T) {
		t.Parallel()

		sessionID, err := types.SessionIDOf("session-1")
		if err != nil {
			t.Fatalf("SessionIDOf() error = %v", err)
		}
		startAgent, err := types.AgentOf("claude")
		if err != nil {
			t.Fatalf("AgentOf() error = %v", err)
		}

		stub := &eventRepositoryStub{}
		finder := &sessionStartedEventFinderStub{
			event: model.EventOf(
				mustEventID(t, "event-started-2"),
				types.EventKindSessionStarted,
				"hook",
				startAgent,
				sessionID,
				"repo-from-start",
				"session started",
				mustTime(t),
			),
		}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub, finder)

		got, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			Client:        "cli",
			Agent:         "codex",
			DefaultClient: "cli",
			DefaultAgent:  "manual",
			SessionID:     "session-1",
			Repo:          "repo-explicit",
			Kind:          types.EventKindSessionEnded,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got.Client() != "cli" {
			t.Fatalf("Client() = %q, want %q", got.Client(), "cli")
		}
		if got.Agent().String() != "codex" {
			t.Fatalf("Agent() = %q, want %q", got.Agent(), "codex")
		}
		if got.Repo() != "repo-explicit" {
			t.Fatalf("Repo() = %q, want %q", got.Repo(), "repo-explicit")
		}
	})

	t.Run("session_started が見つからない場合は fallback を使う", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		finder := &sessionStartedEventFinderStub{err: usecase.ErrSessionStartedEventNotFound}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub, finder)

		got, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			DefaultClient: "cli",
			DefaultAgent:  "manual",
			SessionID:     "session-1",
			Kind:          types.EventKindSessionEnded,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got.Client() != "cli" {
			t.Fatalf("Client() = %q, want %q", got.Client(), "cli")
		}
		if got.Agent().String() != "manual" {
			t.Fatalf("Agent() = %q, want %q", got.Agent(), "manual")
		}
	})

	t.Run("session_started 取得エラーはそのまま返す", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		finder := &sessionStartedEventFinderStub{err: errors.New("boom")}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub, finder)

		_, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			DefaultClient: "cli",
			DefaultAgent:  "manual",
			SessionID:     "session-1",
			Kind:          types.EventKindSessionEnded,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}

type sessionStartedEventFinderStub struct {
	event *model.Event
	err   error
}

func (s *sessionStartedEventFinderStub) FindSessionStartedEvent(
	_ context.Context,
	_ types.SessionID,
) (*model.Event, error) {
	return s.event, s.err
}

func mustEventID(t *testing.T, value string) types.EventID {
	t.Helper()

	eventID, err := types.EventIDOf(value)
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}

	return eventID
}

func mustTime(t *testing.T) time.Time {
	t.Helper()

	return time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)
}

type sessionSaverStub struct {
	called  bool
	session *model.Session
	err     error
}

func (s *sessionSaverStub) SaveSession(_ context.Context, session *model.Session) error {
	s.called = true
	s.session = session
	return s.err
}

func TestRecordSessionBoundaryUsecase_Run_SessionSaver(t *testing.T) {
	t.Parallel()

	t.Run("session start で SessionSaver が呼ばれる", func(t *testing.T) {
		t.Parallel()

		eventStub := &eventRepositoryStub{}
		sessionStub := &sessionSaverStub{}
		sut := usecase.NewRecordSessionBoundaryUsecase(eventStub, nil, sessionStub)

		_, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			Client: "cli",
			Agent:  "claude",
			Repo:   "duck8823/traceary",
			Kind:   types.EventKindSessionStarted,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if !sessionStub.called {
			t.Fatalf("SessionSaver.SaveSession() was not called")
		}
		if sessionStub.session.EndedAt() != nil {
			t.Fatalf("session.EndedAt() should be nil for start")
		}
	})

	t.Run("session end で SessionSaver が呼ばれる", func(t *testing.T) {
		t.Parallel()

		eventStub := &eventRepositoryStub{}
		sessionStub := &sessionSaverStub{}
		sut := usecase.NewRecordSessionBoundaryUsecase(eventStub, nil, sessionStub)

		_, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			Client:    "cli",
			Agent:     "claude",
			Repo:      "duck8823/traceary",
			SessionID: "test-session",
			Kind:      types.EventKindSessionEnded,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if !sessionStub.called {
			t.Fatalf("SessionSaver.SaveSession() was not called")
		}
		if sessionStub.session.EndedAt() == nil {
			t.Fatalf("session.EndedAt() should not be nil for end")
		}
	})

	t.Run("SessionSaver がエラーを返したら Run もエラーを返す", func(t *testing.T) {
		t.Parallel()

		eventStub := &eventRepositoryStub{}
		sessionStub := &sessionSaverStub{err: errors.New("save failed")}
		sut := usecase.NewRecordSessionBoundaryUsecase(eventStub, nil, sessionStub)

		_, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			Client: "cli",
			Agent:  "claude",
			Repo:   "duck8823/traceary",
			Kind:   types.EventKindSessionStarted,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
