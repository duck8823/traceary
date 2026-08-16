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

// stampHeadlessUsageObservedAt keeps a provider timestamp when it is a real
// instant and otherwise uses the wall clock. Unix epoch must not land in the
// ledger: period reports treat it as outside every real interval.
func stampHeadlessUsageObservedAt(sampleTime time.Time) time.Time {
	if sampleTime.Unix() > 0 {
		return sampleTime.UTC()
	}
	return time.Now().UTC()
}
