package usecase_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type eventRepositoryStub struct {
	savedEvent   *model.Event
	err          error
}

func (s *eventRepositoryStub) Save(_ context.Context, event *model.Event) error {
	s.savedEvent = event
	return s.err
}

func (s *eventRepositoryStub) SaveWithAudit(_ context.Context, event *model.Event, _ *model.CommandAudit) error {
	s.savedEvent = event
	return s.err
}

func TestEventUsecase_Log(t *testing.T) {
	t.Parallel()

	t.Run("saves event successfully", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		got, err := sut.Log(context.Background(),
			"  hello traceary  ",
			types.EventKind(""),
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
		)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		if got == nil {
			t.Fatalf("Log() event is nil")
		}
		if stub.savedEvent == nil {
			t.Fatalf("Save() event is nil")
		}
		if got != stub.savedEvent {
			t.Fatalf("saved event mismatch")
		}
		if got.EventID().String() == "" {
			t.Fatalf("EventID() is empty")
		}
		if diff := cmp.Diff("hello traceary", got.Body()); diff != "" {
			t.Fatalf("Body() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Client("cli"), got.Client()); diff != "" {
			t.Fatalf("Client() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("codex", got.Agent().String()); diff != "" {
			t.Fatalf("Agent() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("session-1", got.SessionID().String()); diff != "" {
			t.Fatalf("SessionID() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Workspace("duck8823/traceary"), got.Workspace()); diff != "" {
			t.Fatalf("Workspace() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("saves event with specified kind", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		got, err := sut.Log(context.Background(),
			"compact summary text",
			types.EventKind("compact_summary"),
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
		)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		if diff := cmp.Diff("compact_summary", got.Kind().String()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("defaults to note when kind is empty", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		got, err := sut.Log(context.Background(),
			"hello",
			types.EventKind(""),
			types.Client("cli"),
			types.Agent("manual"),
			types.SessionID("session-1"),
			types.Workspace(""),
		)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		if diff := cmp.Diff("note", got.Kind().String()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns error for invalid kind", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, err := sut.Log(context.Background(),
			"hello",
			types.EventKind("invalid_kind"),
			types.Client("cli"),
			types.Agent("manual"),
			types.SessionID("session-1"),
			types.Workspace(""),
		)
		if err == nil {
			t.Fatalf("Log() error = nil, want error for invalid kind")
		}
	})

	t.Run("returns error for invalid required fields", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, err := sut.Log(context.Background(),
			"hello",
			types.EventKind(""),
			types.Client(""),
			types.Agent(""),
			types.SessionID("session-1"),
			types.Workspace(""),
		)
		if err == nil {
			t.Fatalf("Log() error = nil, want error")
		}
		if stub.savedEvent != nil {
			t.Fatalf("Save() should not be called")
		}
	})
}
