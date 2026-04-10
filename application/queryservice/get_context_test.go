package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type contextEventFinderStub struct {
	receivedPath  string
	receivedInput queryservice.GetContextInput
	events        []*model.Event
	err           error
}

func (s *contextEventFinderStub) GetContextEvents(
	_ context.Context,
	dbPath string,
	input queryservice.GetContextInput,
) ([]*model.Event, error) {
	s.receivedPath = dbPath
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

		got, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.GetContextInput{
			Repo:      "github.com/duck8823/traceary",
			SessionID: "session-1",
			Limit:     10,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedPath != "/tmp/traceary.db" {
			t.Fatalf("received path = %q, want %q", stub.receivedPath, "/tmp/traceary.db")
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

		_, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.GetContextInput{Limit: 0})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
