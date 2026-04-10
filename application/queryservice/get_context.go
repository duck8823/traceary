package queryservice

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
)

// GetContextQueryService returns contextual events.
type GetContextQueryService interface {
	// Run executes the context query.
	Run(ctx context.Context, dbPath string, input port.GetContextInput) ([]*model.Event, error)
}

type getContextQueryService struct {
	contextEventFinder port.ContextEventFinder
}

// NewGetContextQueryService creates a GetContextQueryService.
func NewGetContextQueryService(contextEventFinder port.ContextEventFinder) GetContextQueryService {
	return &getContextQueryService{contextEventFinder: contextEventFinder}
}

// Run executes the context query.
func (s *getContextQueryService) Run(
	ctx context.Context,
	dbPath string,
	input port.GetContextInput,
) ([]*model.Event, error) {
	if s.contextEventFinder == nil {
		return nil, xerrors.Errorf("context event finder is not configured")
	}
	if strings.TrimSpace(dbPath) == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}

	events, err := s.contextEventFinder.GetContextEvents(ctx, dbPath, input)
	if err != nil {
		return nil, xerrors.Errorf("failed to get context events: %w", err)
	}

	return events, nil
}
