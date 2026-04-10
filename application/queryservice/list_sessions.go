package queryservice

import (
	"context"
	"time"

	"golang.org/x/xerrors"
)

// SessionSummary holds aggregated information about a single session.
type SessionSummary struct {
	SessionID       string
	Repo            string
	StartedAt       time.Time
	EndedAt         *time.Time
	Status          string
	TotalEvents     int
	CommandCount    int
	Agents          []string
	Label           string
	Summary         string
	ParentSessionID string
}

// ListSessionsInput is the input for session listing.
type ListSessionsInput struct {
	Limit  int
	Offset int
	Repo   string
	Agent  string
	From   *time.Time
	To     *time.Time
}

// SessionSummaryFinder provides session summary lookup.
type SessionSummaryFinder interface {
	ListSessionSummaries(ctx context.Context, dbPath string, input ListSessionsInput) ([]*SessionSummary, error)
}

// ListSessionsQueryService returns session summaries.
type ListSessionsQueryService interface {
	Run(ctx context.Context, dbPath string, input ListSessionsInput) ([]*SessionSummary, error)
}

type listSessionsQueryService struct {
	finder SessionSummaryFinder
}

// NewListSessionsQueryService creates a ListSessionsQueryService.
func NewListSessionsQueryService(finder SessionSummaryFinder) ListSessionsQueryService {
	return &listSessionsQueryService{finder: finder}
}

func (s *listSessionsQueryService) Run(
	ctx context.Context,
	dbPath string,
	input ListSessionsInput,
) ([]*SessionSummary, error) {
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
