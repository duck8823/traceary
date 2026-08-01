package sqlite

import (
	"context"
	"errors"
	"time"

	"github.com/duck8823/traceary/application/types"
)

// Scrub is retained only for internal legacy security tests.
func (a *PayloadRehearsalAdapter) Scrub(ctx context.Context, c types.PayloadRehearsalConfig) (result types.PayloadRehearsalMetrics, resultErr error) {
	h, result, err := a.PrepareScrub(ctx, c)
	if err != nil {
		return result, err
	}
	completed := false
	defer func() {
		if !completed {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			if releaseErr := a.ReleaseScrub(releaseCtx, h); resultErr == nil {
				resultErr = releaseErr
			}
		}
		if closeErr := a.CloseScrub(h); resultErr == nil {
			resultErr = closeErr
		}
	}()
	deadline := time.Now().Add(c.ScrubTimeLimit)
	for _, field := range types.OrderedPayloadRehearsalFields() {
		for time.Now().Before(deadline) && result.StoredBytes < c.ScrubByteLimit {
			var done bool
			result, done, err = a.AdvanceScrubField(ctx, h, field)
			if err != nil {
				return result, err
			}
			if done {
				break
			}
		}
		if !time.Now().Before(deadline) || result.StoredBytes >= c.ScrubByteLimit {
			return result, errors.New("scrub resource cap reached; resume scrub with the persisted checkpoint")
		}
	}
	result, err = a.CompleteScrub(ctx, h)
	completed = err == nil
	return result, err
}
