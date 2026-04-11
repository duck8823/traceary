package usecase_test

import (
	"context"
	"testing"

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
		if got.Body() != "hello traceary" {
			t.Fatalf("Body() = %q, want %q", got.Body(), "hello traceary")
		}
		if got.Client() != "cli" {
			t.Fatalf("Client() = %q, want %q", got.Client(), "cli")
		}
		if got.Agent().String() != "codex" {
			t.Fatalf("Agent() = %q, want %q", got.Agent(), "codex")
		}
		if got.SessionID().String() != "session-1" {
			t.Fatalf("SessionID() = %q, want %q", got.SessionID(), "session-1")
		}
		if got.Workspace() != "duck8823/traceary" {
			t.Fatalf("Repo() = %q, want %q", got.Workspace(), "duck8823/traceary")
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
