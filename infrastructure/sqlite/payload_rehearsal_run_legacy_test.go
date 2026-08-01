package sqlite

import (
	"context"

	"github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

// Run routes legacy white-box tests through the production orchestrator.
//
//nolint:wrapcheck // test adapter preserves the production use case result.
func (a *PayloadRehearsalAdapter) Run(ctx context.Context, c types.PayloadRehearsalConfig, command types.PayloadRehearsalRunCommand) (result types.PayloadRehearsalMetrics, resultErr error) {
	u := usecase.NewPayloadRehearsalUsecase(a, a, a, a)
	if command.IsResume() {
		return u.Resume(ctx, c)
	}
	return u.Run(ctx, c)
}
