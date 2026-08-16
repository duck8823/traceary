package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// resolveUnavailableObservationTime stamps a stop-hook-family unavailable
// observation. A later exact replay must reuse the first write's time so
// descriptor identity still reconciles. Unix epoch is never a first-write
// stamp: period-windowed reports treat it as outside every real interval.
func resolveUnavailableObservationTime(
	ctx context.Context,
	repo model.UsageObservationRepository,
	id types.UsageObservationID,
	preferred time.Time,
) (time.Time, error) {
	if repo == nil {
		return time.Time{}, xerrors.Errorf("usage repository must be configured")
	}
	existing, err := repo.FindByID(ctx, id)
	if err != nil {
		return time.Time{}, xerrors.Errorf("failed to inspect existing usage observation: %w", err)
	}
	if current, present := existing.Value(); present {
		return current.Descriptor().ObservedAt(), nil
	}
	if preferred.Unix() > 0 {
		return preferred.UTC(), nil
	}
	return time.Now().UTC(), nil
}
