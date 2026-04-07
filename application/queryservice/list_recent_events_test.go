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
	receivedLimit int
	events        []*model.Event
	err           error
}

func (s *recentEventFinderStub) ListRecent(_ context.Context, dbPath string, limit int) ([]*model.Event, error) {
	s.receivedPath = dbPath
	s.receivedLimit = limit
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

		got, err := sut.Run(context.Background(), "/tmp/traceary.db", 5)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedPath != "/tmp/traceary.db" {
			t.Fatalf("received path = %q, want %q", stub.receivedPath, "/tmp/traceary.db")
		}
		if stub.receivedLimit != 5 {
			t.Fatalf("received limit = %d, want %d", stub.receivedLimit, 5)
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

		_, err := sut.Run(context.Background(), "/tmp/traceary.db", 0)
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
