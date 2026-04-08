package queryservice

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// ListRecentEventsInput is the input for recent event listing.
type ListRecentEventsInput struct {
	Limit  int
	Offset int
}

// RecentEventFinder provides recent event lookup.
type RecentEventFinder interface {
	// ListRecent returns events in descending time order.
	ListRecent(ctx context.Context, dbPath string, input ListRecentEventsInput) ([]*model.Event, error)
}

// ListRecentEventsQueryService returns recent events.
type ListRecentEventsQueryService interface {
	// Run executes the recent event query.
	Run(ctx context.Context, dbPath string, input ListRecentEventsInput) ([]*model.Event, error)
}

type listRecentEventsQueryService struct {
	recentEventFinder RecentEventFinder
}

// NewListRecentEventsQueryService creates a ListRecentEventsQueryService.
func NewListRecentEventsQueryService(recentEventFinder RecentEventFinder) ListRecentEventsQueryService {
	return &listRecentEventsQueryService{recentEventFinder: recentEventFinder}
}

// Run executes the recent event query.
func (s *listRecentEventsQueryService) Run(
	ctx context.Context,
	dbPath string,
	input ListRecentEventsInput,
) ([]*model.Event, error) {
	if s.recentEventFinder == nil {
		return nil, xerrors.Errorf("recent event finder is not configured")
	}
	if dbPath == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if input.Offset < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	events, err := s.recentEventFinder.ListRecent(ctx, dbPath, input)
	if err != nil {
		return nil, xerrors.Errorf("failed to list recent events: %w", err)
	}

	return events, nil
}
