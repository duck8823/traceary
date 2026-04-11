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

type contextEventFinderStub struct {
	receivedInput port.GetContextInput
	events        []*model.Event
	err           error
}

func (s *contextEventFinderStub) GetContextEvents(
	_ context.Context,
	input port.GetContextInput,
) ([]*model.Event, error) {
	s.receivedInput = input
	return s.events, s.err
}

func TestGetContextQueryService_Run(t *testing.T) {
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

	t.Run("returns context events", func(t *testing.T) {
		t.Parallel()

		stub := &contextEventFinderStub{
			events: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"mcp",
					agent,
					sessionID,
					"github.com/duck8823/traceary",
					"hello traceary",
					time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
				),
			},
		}
		sut := queryservice.NewGetContextQueryService(stub)

		got, err := sut.Run(context.Background(), port.GetContextInput{
			Repo:      "github.com/duck8823/traceary",
			SessionID: "session-1",
			Limit:     10,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedInput.SessionID != "session-1" {
			t.Fatalf("received session ID = %q, want %q", stub.receivedInput.SessionID, "session-1")
		}
		if len(got) != 1 {
			t.Fatalf("len(events) = %d, want 1", len(got))
		}
	})

	t.Run("limit が不正ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewGetContextQueryService(&contextEventFinderStub{})

		_, err := sut.Run(context.Background(), port.GetContextInput{Limit: 0})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
