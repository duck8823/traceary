package sqlite

import (
	"context"

	"github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

// Scrub routes legacy white-box tests through the production orchestrator.
//
//nolint:wrapcheck // test adapter preserves the production use case result.
func (a *PayloadRehearsalAdapter) Scrub(ctx context.Context, c types.PayloadRehearsalConfig) (result types.PayloadRehearsalMetrics, resultErr error) {
	return usecase.NewPayloadRehearsalUsecase(a, a, a, a).Scrub(ctx, c)
}
