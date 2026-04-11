package queryservice

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

// ListTimelineBlocksQueryService returns timeline blocks based on gap detection.
type ListTimelineBlocksQueryService interface {
	// Run executes the timeline block query.
	Run(ctx context.Context, input port.ListTimelineBlocksInput) ([]*port.TimelineBlock, error)
}

type listTimelineBlocksQueryService struct {
	finder port.TimelineBlockFinder
}

// NewListTimelineBlocksQueryService creates a ListTimelineBlocksQueryService.
func NewListTimelineBlocksQueryService(finder port.TimelineBlockFinder) ListTimelineBlocksQueryService {
	return &listTimelineBlocksQueryService{finder: finder}
}

// Run executes the timeline block query.
func (s *listTimelineBlocksQueryService) Run(
	ctx context.Context,
	input port.ListTimelineBlocksInput,
) ([]*port.TimelineBlock, error) {
	if s.finder == nil {
		return nil, xerrors.Errorf("timeline block finder is not configured")
	}
	if input.GapSeconds <= 0 {
		return nil, xerrors.Errorf("gap must be greater than 0")
	}
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}

	blocks, err := s.finder.ListTimelineBlocks(ctx, input)
	if err != nil {
		return nil, xerrors.Errorf("failed to list timeline blocks: %w", err)
	}

	return blocks, nil
}
