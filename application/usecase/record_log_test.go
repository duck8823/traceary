package usecase_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
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

func TestRecordLogUsecase_Run(t *testing.T) {
	t.Parallel()

	t.Run("saves event successfully", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordLogUsecase(stub)

		got, err := sut.Run(context.Background(), usecase.RecordLogInput{
			Message:   "  hello traceary  ",
			Client:    " cli ",
			Agent:     "codex",
			SessionID: "session-1",
			Workspace:      "  duck8823/traceary  ",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got == nil {
			t.Fatalf("Run() event is nil")
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
		if diff := cmp.Diff("cli", got.Client()); diff != "" {
			t.Fatalf("Client() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("codex", got.Agent().String()); diff != "" {
			t.Fatalf("Agent() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("session-1", got.SessionID().String()); diff != "" {
			t.Fatalf("SessionID() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("duck8823/traceary", got.Workspace()); diff != "" {
			t.Fatalf("Workspace() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("saves event with specified kind", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordLogUsecase(stub)

		got, err := sut.Run(context.Background(), usecase.RecordLogInput{
			Message:   "compact summary text",
			Kind:      "compact_summary",
			Client:    "hook",
			Agent:     "claude",
			SessionID: "session-1",
			Workspace: "duck8823/traceary",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if diff := cmp.Diff("compact_summary", got.Kind().String()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("defaults to note when kind is empty", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordLogUsecase(stub)

		got, err := sut.Run(context.Background(), usecase.RecordLogInput{
			Message:   "hello",
			Client:    "cli",
			Agent:     "manual",
			SessionID: "session-1",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if diff := cmp.Diff("note", got.Kind().String()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns error for invalid kind", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordLogUsecase(stub)

		_, err := sut.Run(context.Background(), usecase.RecordLogInput{
			Message:   "hello",
			Kind:      "invalid_kind",
			Client:    "cli",
			Agent:     "manual",
			SessionID: "session-1",
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error for invalid kind")
		}
	})

	t.Run("returns error for invalid required fields", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordLogUsecase(stub)

		_, err := sut.Run(context.Background(), usecase.RecordLogInput{
			Message:   "hello",
			Agent:     "",
			SessionID: "session-1",
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
		if stub.savedEvent != nil {
			t.Fatalf("Save() should not be called")
		}
	})
}
