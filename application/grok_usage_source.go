package application

import (
	"io"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// GrokUsageCounters preserves presence from the verified terminal end.usage
// contract. Nil is unavailable; a pointer to zero is known zero.
type GrokUsageCounters struct {
	InputTokens       *int64
	CachedInputTokens *int64
	OutputTokens      *int64
	ReasoningTokens   *int64
	TotalTokens       *int64
}

// GrokUsageSample is one body-free terminal request total.
type GrokUsageSample struct {
	RecordID      string
	SourceName    string
	SourceVersion string
	Model         string
	ObservedAt    time.Time
	TerminalCode  types.UsageTerminalCode
	Available     bool
	Counters      GrokUsageCounters
}

// GrokUsageLoadResult contains one authoritative terminal result. A terminal
// may be observed without a usage object; its provider identity remains
// available so unavailable evidence is portable across Traceary sessions.
type GrokUsageLoadResult struct {
	Samples          []GrokUsageSample
	BoundaryObserved bool
	TerminalRecordID string
	TerminalCode     types.UsageTerminalCode
}

// GrokUsageRepository persists provider-neutral observations idempotently.
type GrokUsageRepository interface {
	model.UsageObservationRepository
}

// GrokHeadlessUsageStream forwards a Traceary-owned Grok streaming-json
// response while retaining only terminal body-free usage metadata.
type GrokHeadlessUsageStream interface {
	io.Writer
	Complete() (GrokUsageLoadResult, error)
}

// GrokHeadlessUsageStreamFactory creates one bounded adapter per run.
type GrokHeadlessUsageStreamFactory interface {
	New(io.Writer) GrokHeadlessUsageStream
}
