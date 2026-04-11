package queryservice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/port"
)

type timelineBlockFinderStub struct {
	blocks []*port.TimelineBlock
	err    error
}

func (s *timelineBlockFinderStub) ListTimelineBlocks(
	_ context.Context,
	_ port.ListTimelineBlocksInput,
) ([]*port.TimelineBlock, error) {
	return s.blocks, s.err
}

func TestListTimelineBlocksQueryService_Run(t *testing.T) {
	t.Parallel()

	t.Run("returns blocks from finder", func(t *testing.T) {
		t.Parallel()

		stub := &timelineBlockFinderStub{
			blocks: []*port.TimelineBlock{
				{
					BlockStart: time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC),
					BlockEnd:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
					EventCount: 10,
				},
			},
		}
		sut := queryservice.NewListTimelineBlocksQueryService(stub)

		blocks, err := sut.Run(context.Background(), port.ListTimelineBlocksInput{
			GapSeconds: 900,
			Limit:      20,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("len(blocks) = %d, want 1", len(blocks))
		}
		if blocks[0].EventCount != 10 {
			t.Fatalf("EventCount = %d, want 10", blocks[0].EventCount)
		}
	})

	t.Run("returns error for gap <= 0", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewListTimelineBlocksQueryService(&timelineBlockFinderStub{})

		_, err := sut.Run(context.Background(), port.ListTimelineBlocksInput{
			GapSeconds: 0,
			Limit:      20,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error for gap <= 0")
		}
	})

	t.Run("returns error for limit <= 0", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewListTimelineBlocksQueryService(&timelineBlockFinderStub{})

		_, err := sut.Run(context.Background(), port.ListTimelineBlocksInput{
			GapSeconds: 900,
			Limit:      0,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error for limit <= 0")
		}
	})

	t.Run("returns error when finder is nil", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewListTimelineBlocksQueryService(nil)

		_, err := sut.Run(context.Background(), port.ListTimelineBlocksInput{
			GapSeconds: 900,
			Limit:      20,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error for nil finder")
		}
	})

	t.Run("wraps finder error", func(t *testing.T) {
		t.Parallel()

		stub := &timelineBlockFinderStub{
			err: errors.New("db error"),
		}
		sut := queryservice.NewListTimelineBlocksQueryService(stub)

		_, err := sut.Run(context.Background(), port.ListTimelineBlocksInput{
			GapSeconds: 900,
			Limit:      20,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want wrapped error")
		}
	})
}
