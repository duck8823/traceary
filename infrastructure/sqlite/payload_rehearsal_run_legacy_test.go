package sqlite

import (
	"context"
	"time"

	"github.com/duck8823/traceary/application/types"
)

// Run is retained only for internal legacy security tests while production
// callers use the application-owned workflow.
func (a *PayloadRehearsalAdapter) Run(ctx context.Context, c types.PayloadRehearsalConfig, command types.PayloadRehearsalRunCommand) (result types.PayloadRehearsalMetrics, resultErr error) {
	h, result, err := a.Prepare(ctx, c, command)
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := a.Close(h); resultErr == nil {
			resultErr = closeErr
		}
	}()
	deadline := time.Now().Add(c.WallTimeLimit)
	for _, field := range types.OrderedPayloadRehearsalFields() {
		for time.Now().Before(deadline) {
			var done bool
			result, done, err = a.AdvanceField(ctx, h, field)
			if err != nil {
				owned := h.(*payloadRehearsalRunHandle)
				return result, pauseRun(ctx, owned.session, owned.runID, owned.leaseToken, err, owned.guard)
			}
			if done {
				break
			}
		}
		if !time.Now().Before(deadline) {
			return a.Pause(ctx, h)
		}
	}
	return a.Complete(ctx, h)
}
