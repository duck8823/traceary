package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
)

type eventSearcherStub struct {
	receivedInput port.SearchEventsInput
	events        []*model.Event
	err           error
}

func (s *eventSearcherStub) SearchEvents(
	_ context.Context,
	input port.SearchEventsInput,
) ([]*model.Event, error) {
	s.receivedInput = input
	return s.events, s.err
}

func TestSearchEventsQueryService_Run(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDOf("event-1")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-1")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	t.Run("returns search results", func(t *testing.T) {
		t.Parallel()

		stub := &eventSearcherStub{
			events: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"github.com/duck8823/traceary",
					"hello traceary",
					time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
				),
			},
		}
		sut := queryservice.NewSearchEventsQueryService(stub)

		got, err := sut.Run(context.Background(), port.SearchEventsInput{
			Query:     "traceary",
			Repo:      "github.com/duck8823/traceary",
			SessionID: "session-1",
			Client:    "cli",
			Agent:     "codex",
			Kind:      "note",
			Limit:     10,
			Offset:    3,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedInput.Query != "traceary" {
			t.Fatalf("received query = %q, want %q", stub.receivedInput.Query, "traceary")
		}
		if stub.receivedInput.Kind != "note" {
			t.Fatalf("received kind = %q, want %q", stub.receivedInput.Kind, "note")
		}
		if stub.receivedInput.Offset != 3 {
			t.Fatalf("received offset = %d, want %d", stub.receivedInput.Offset, 3)
		}
		if len(got) != 1 {
			t.Fatalf("len(events) = %d, want 1", len(got))
		}
	})

	t.Run("searches with structural filters when query is empty", func(t *testing.T) {
		t.Parallel()

		stub := &eventSearcherStub{}
		sut := queryservice.NewSearchEventsQueryService(stub)

		_, err := sut.Run(context.Background(), port.SearchEventsInput{
			SessionID: "session-1",
			Limit:     10,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedInput.SessionID != "session-1" {
			t.Fatalf("received session_id = %q, want %q", stub.receivedInput.SessionID, "session-1")
		}
	})

	t.Run("returns error when all search conditions are empty", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewSearchEventsQueryService(&eventSearcherStub{})

		_, err := sut.Run(context.Background(), port.SearchEventsInput{
			Query: "   ",
			Limit: 10,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("returns error for unknown kind", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewSearchEventsQueryService(&eventSearcherStub{})

		_, err := sut.Run(context.Background(), port.SearchEventsInput{
			Query: "traceary",
			Kind:  "unknown",
			Limit: 10,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("offset が負ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewSearchEventsQueryService(&eventSearcherStub{})

		_, err := sut.Run(context.Background(), port.SearchEventsInput{
			Query:  "traceary",
			Limit:  10,
			Offset: -1,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("search kind alias audit を受け付ける", func(t *testing.T) {
		t.Parallel()

		stub := &eventSearcherStub{}
		sut := queryservice.NewSearchEventsQueryService(stub)

		_, err := sut.Run(context.Background(), port.SearchEventsInput{
			Query: "go test",
			Kind:  "audit",
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedInput.Kind != "command_executed" {
			t.Fatalf("received kind = %q, want %q", stub.receivedInput.Kind, "command_executed")
		}
	})
}
