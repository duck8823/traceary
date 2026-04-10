package queryservice

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

// ListSessionsQueryService returns session summaries.
type ListSessionsQueryService interface {
	Run(ctx context.Context, dbPath string, input port.ListSessionsInput) ([]*port.SessionSummary, error)
}

type listSessionsQueryService struct {
	finder port.SessionSummaryFinder
}

// NewListSessionsQueryService creates a ListSessionsQueryService.
func NewListSessionsQueryService(finder port.SessionSummaryFinder) ListSessionsQueryService {
	return &listSessionsQueryService{finder: finder}
}

func (s *listSessionsQueryService) Run(
	ctx context.Context,
	dbPath string,
	input port.ListSessionsInput,
) ([]*port.SessionSummary, error) {
	if s.finder == nil {
		return nil, xerrors.Errorf("session summary finder is not configured")
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

	summaries, err := s.finder.ListSessionSummaries(ctx, dbPath, input)
	if err != nil {
		return nil, xerrors.Errorf("failed to list session summaries: %w", err)
	}

	return summaries, nil
}
