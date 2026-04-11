package queryservice

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
)

// ListRecentEventsQueryService returns recent events.
type ListRecentEventsQueryService interface {
	// Run executes the recent event query.
	Run(ctx context.Context, input port.ListRecentEventsInput) ([]*model.Event, error)
}

type listRecentEventsQueryService struct {
	recentEventFinder port.RecentEventFinder
}

// NewListRecentEventsQueryService creates a ListRecentEventsQueryService.
func NewListRecentEventsQueryService(recentEventFinder port.RecentEventFinder) ListRecentEventsQueryService {
	return &listRecentEventsQueryService{recentEventFinder: recentEventFinder}
}

// Run executes the recent event query.
func (s *listRecentEventsQueryService) Run(
	ctx context.Context,
	input port.ListRecentEventsInput,
) ([]*model.Event, error) {
	if s.recentEventFinder == nil {
		return nil, xerrors.Errorf("recent event finder is not configured")
	}
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if input.Offset < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	events, err := s.recentEventFinder.ListRecent(ctx, input)
	if err != nil {
		return nil, xerrors.Errorf("failed to list recent events: %w", err)
	}

	return events, nil
}
