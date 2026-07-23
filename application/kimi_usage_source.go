package application

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/model"
)

// KimiUsageCounters preserves field presence from Kimi's verified
// usage.record contract. Nil is unavailable; a pointer to zero is known zero.
type KimiUsageCounters struct {
	InputOther         *int64
	InputCacheRead     *int64
	InputCacheCreation *int64
	Output             *int64
}

// KimiUsageSample is one body-free, partial source record from a session wire.
type KimiUsageSample struct {
	RecordID      string
	SourceName    string
	SourceVersion string
	Model         string
	ObservedAt    time.Time
	Counters      KimiUsageCounters
}

// KimiUsageLoadResult contains the verified source rows and the latest stable
// turn ordinal observed without decoding prompt or content bodies.
type KimiUsageLoadResult struct {
	Samples           []KimiUsageSample
	LatestTurnOrdinal int64
}

// KimiUsageSource loads body-free usage metadata for one provider session.
type KimiUsageSource interface {
	Load(context.Context, string) (KimiUsageLoadResult, error)
}

// KimiUsageRepository persists provider-neutral observations idempotently.
type KimiUsageRepository interface {
	model.UsageObservationRepository
}
