package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type recentEventFinderStub struct {
	receivedPath  string
	receivedInput queryservice.ListRecentEventsInput
	events        []*model.Event
	err           error
}

func (s *recentEventFinderStub) ListRecent(
	_ context.Context,
	dbPath string,
	input queryservice.ListRecentEventsInput,
) ([]*model.Event, error) {
	s.receivedPath = dbPath
	s.receivedInput = input
	return s.events, s.err
}

func TestListRecentEventsQueryService_Run(t *testing.T) {
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

	t.Run("イベント一覧を返す", func(t *testing.T) {
		t.Parallel()

		stub := &recentEventFinderStub{
			events: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"duck8823/traceary",
					"hello",
					time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
				),
			},
		}
		sut := queryservice.NewListRecentEventsQueryService(stub)

		got, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.ListRecentEventsInput{
			Limit:     5,
			Offset:    2,
			Kind:      "note",
			Client:    "cli",
			Agent:     "codex",
			SessionID: "session-1",
			Repo:      "duck8823/traceary",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedPath != "/tmp/traceary.db" {
			t.Fatalf("received path = %q, want %q", stub.receivedPath, "/tmp/traceary.db")
		}
		if stub.receivedInput.Limit != 5 {
			t.Fatalf("received limit = %d, want %d", stub.receivedInput.Limit, 5)
		}
		if stub.receivedInput.Offset != 2 {
			t.Fatalf("received offset = %d, want %d", stub.receivedInput.Offset, 2)
		}
		if stub.receivedInput.Kind != "note" {
			t.Fatalf("received kind = %q, want %q", stub.receivedInput.Kind, "note")
		}
		if stub.receivedInput.SessionID != "session-1" {
			t.Fatalf("received session_id = %q, want %q", stub.receivedInput.SessionID, "session-1")
		}
		if len(got) != 1 {
			t.Fatalf("len(events) = %d, want 1", len(got))
		}
		if got[0].Body() != "hello" {
			t.Fatalf("Body() = %q, want %q", got[0].Body(), "hello")
		}
	})

	t.Run("limit が 0 以下ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewListRecentEventsQueryService(&recentEventFinderStub{})

		_, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.ListRecentEventsInput{})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("offset が負ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewListRecentEventsQueryService(&recentEventFinderStub{})

		_, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.ListRecentEventsInput{
			Limit:  10,
			Offset: -1,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
