package queryservice

import (
	"context"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

// LiteralSearchPageSource is the persistence-facing canonical search port.
type LiteralSearchPageSource interface {
	SearchLiteralPage(context.Context, apptypes.LiteralSearchRequest) (apptypes.LiteralSearchPage, error)
}

// LiteralSearchService owns the application boundary for tiered literal pages.
type LiteralSearchService struct{ source LiteralSearchPageSource }

// NewLiteralSearchService constructs the validated tiered search service.
func NewLiteralSearchService(source LiteralSearchPageSource) *LiteralSearchService {
	return &LiteralSearchService{source: source}
}

// SearchLiteralPage validates protocol-neutral invariants before persistence.
func (s *LiteralSearchService) SearchLiteralPage(ctx context.Context, request apptypes.LiteralSearchRequest) (apptypes.LiteralSearchPage, error) {
	if err := request.Validate(); err != nil {
		return apptypes.LiteralSearchPage{}, fmt.Errorf("validate literal search request: %w", err)
	}
	page, err := s.source.SearchLiteralPage(ctx, request)
	if err != nil {
		return apptypes.LiteralSearchPage{}, fmt.Errorf("search canonical literal page: %w", err)
	}
	return page, nil
}

var _ TieredEventSearchQuery = (*LiteralSearchService)(nil)
