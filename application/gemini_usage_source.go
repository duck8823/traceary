package application

import (
	"io"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// GeminiUsageCounters preserves field presence from the verified terminal
// stream contract. Nil is unavailable and a pointer to zero is known zero.
type GeminiUsageCounters struct {
	InputTokens       *int64
	CachedInputTokens *int64
	OutputTokens      *int64
	TotalTokens       *int64
}

// GeminiUsageSample is one body-free terminal run total. When the result
// reports model totals, each model is emitted separately and the aggregate
// total is not emitted again.
type GeminiUsageSample struct {
	RecordID      string
	SourceName    string
	SourceVersion string
	Model         string
	ObservedAt    time.Time
	TerminalCode  types.UsageTerminalCode
	Available     bool
	Counters      GeminiUsageCounters
}

// GeminiUsageLoadResult contains one authoritative terminal result.
type GeminiUsageLoadResult struct {
	Samples          []GeminiUsageSample
	BoundaryObserved bool
}

// GeminiUsageRepository persists provider-neutral observations idempotently.
type GeminiUsageRepository interface {
	model.UsageObservationRepository
}

// GeminiHeadlessUsageStream forwards a Traceary-owned Gemini JSON stream while
// retaining only terminal body-free usage metadata.
type GeminiHeadlessUsageStream interface {
	io.Writer
	Complete() (GeminiUsageLoadResult, error)
}

// GeminiHeadlessUsageStreamFactory creates one bounded adapter per run.
type GeminiHeadlessUsageStreamFactory interface {
	New(io.Writer) GeminiHeadlessUsageStream
}
