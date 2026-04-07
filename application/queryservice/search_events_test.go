package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type eventSearcherStub struct {
	receivedPath  string
	receivedInput queryservice.SearchEventsInput
	events        []*model.Event
	err           error
}

func (s *eventSearcherStub) SearchEvents(
	_ context.Context,
	dbPath string,
	input queryservice.SearchEventsInput,
) ([]*model.Event, error) {
	s.receivedPath = dbPath
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

	t.Run("検索結果を返す", func(t *testing.T) {
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

		got, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.SearchEventsInput{
			Query:     "traceary",
			Repo:      "github.com/duck8823/traceary",
			SessionID: "session-1",
			Client:    "cli",
			Agent:     "codex",
			Kind:      "note",
			Limit:     10,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedPath != "/tmp/traceary.db" {
			t.Fatalf("received path = %q, want %q", stub.receivedPath, "/tmp/traceary.db")
		}
		if stub.receivedInput.Query != "traceary" {
			t.Fatalf("received query = %q, want %q", stub.receivedInput.Query, "traceary")
		}
		if stub.receivedInput.Kind != "note" {
			t.Fatalf("received kind = %q, want %q", stub.receivedInput.Kind, "note")
		}
		if len(got) != 1 {
			t.Fatalf("len(events) = %d, want 1", len(got))
		}
	})

	t.Run("検索語が空でも構造フィルタがあれば検索できる", func(t *testing.T) {
		t.Parallel()

		stub := &eventSearcherStub{}
		sut := queryservice.NewSearchEventsQueryService(stub)

		_, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.SearchEventsInput{
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

	t.Run("検索条件が空ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewSearchEventsQueryService(&eventSearcherStub{})

		_, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.SearchEventsInput{
			Query: "   ",
			Limit: 10,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("未知の kind ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewSearchEventsQueryService(&eventSearcherStub{})

		_, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.SearchEventsInput{
			Query: "traceary",
			Kind:  "unknown",
			Limit: 10,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
