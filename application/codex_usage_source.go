package application

import (
	"context"
	"io"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// CodexUsageLoadCriteria selects one host-owned Codex session transcript.
type CodexUsageLoadCriteria struct {
	SessionID types.SessionID
}

// CodexUsageCounters preserves field presence from the verified Codex source.
// A nil field is unavailable; a pointer to zero is known zero.
type CodexUsageCounters struct {
	InputTokens           *int64
	CachedInputTokens     *int64
	CacheWriteInputTokens *int64
	OutputTokens          *int64
	ReasoningOutputTokens *int64
	TotalTokens           *int64
}

// CodexUsageSample is one body-free, authoritative terminal-turn record.
type CodexUsageSample struct {
	RecordID      string
	SuppressionID string
	SourceName    string
	SourceVersion string
	Model         string
	ObservedAt    time.Time
	TerminalCode  types.UsageTerminalCode
	Available     bool
	Counters      CodexUsageCounters
}

// CodexUsageLoadResult contains terminal turns currently visible in the
// selected source. Empty is a supported explicit outcome.
type CodexUsageLoadResult struct {
	Samples          []CodexUsageSample
	BoundaryObserved bool
}

// CodexUsageRepository atomically records normal observations and chooses one
// additive winner among equivalent headless/rollout source alternatives.
type CodexUsageRepository interface {
	model.UsageObservationRepository
	RecordExclusive(
		ctx context.Context,
		key types.UsageExclusivityKey,
		additive, excluded *model.UsageObservation,
	) (model.UsageObservationTransition, error)
}

// CodexUsageSource reads only body-free usage metadata from local Codex files.
type CodexUsageSource interface {
	Load(ctx context.Context, criteria CodexUsageLoadCriteria) (CodexUsageLoadResult, error)
}

// CodexHeadlessUsageStream forwards a Traceary-owned Codex exec JSON stream
// while retaining only body-free terminal usage metadata in memory.
type CodexHeadlessUsageStream interface {
	io.Writer
	Complete() (CodexUsageLoadResult, error)
}

// CodexHeadlessUsageStreamFactory creates one bounded stream adapter per
// supervised Codex exec invocation.
type CodexHeadlessUsageStreamFactory interface {
	New(destination io.Writer) CodexHeadlessUsageStream
}
