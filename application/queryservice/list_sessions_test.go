package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/port"
)

type sessionSummaryFinderStub struct {
	receivedInput port.ListSessionsInput
	summaries     []*port.SessionSummary
	err           error
}

func (s *sessionSummaryFinderStub) ListSessionSummaries(
	_ context.Context,
	input port.ListSessionsInput,
) ([]*port.SessionSummary, error) {
	s.receivedInput = input
	return s.summaries, s.err
}

func TestListSessionsQueryService_Run(t *testing.T) {
	t.Parallel()

	t.Run("returns session summaries", func(t *testing.T) {
		t.Parallel()

		stub := &sessionSummaryFinderStub{
			summaries: []*port.SessionSummary{
				{
					SessionID:    "s1",
					StartedAt:    time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
					TotalEvents:  5,
					CommandCount: 3,
					Agents:       []string{"claude"},
				},
			},
		}
		sut := queryservice.NewListSessionsQueryService(stub)

		got, err := sut.Run(context.Background(), port.ListSessionsInput{
			Limit: 10,
			Repo:  "duck8823/traceary",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if stub.receivedInput.Repo != "duck8823/traceary" {
			t.Fatalf("received repo = %q, want %q", stub.receivedInput.Repo, "duck8823/traceary")
		}
	})

	t.Run("finder が nil ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewListSessionsQueryService(nil)
		_, err := sut.Run(context.Background(), port.ListSessionsInput{Limit: 10})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("limit が 0 ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewListSessionsQueryService(&sessionSummaryFinderStub{})
		_, err := sut.Run(context.Background(), port.ListSessionsInput{Limit: 0})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("offset が負ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewListSessionsQueryService(&sessionSummaryFinderStub{})
		_, err := sut.Run(context.Background(), port.ListSessionsInput{Limit: 10, Offset: -1})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
