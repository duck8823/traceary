package application

import (
	"context"
	"io"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// ClaudeUsageCaptureMode fixes the authoritative representation for a Claude run.
type ClaudeUsageCaptureMode string

const (
	// ClaudeUsageModeTranscriptCalls records unique interactive assistant requests.
	ClaudeUsageModeTranscriptCalls ClaudeUsageCaptureMode = "transcript_calls"
	// ClaudeUsageModeOneShotStream records only the terminal one-shot result.
	ClaudeUsageModeOneShotStream ClaudeUsageCaptureMode = "one_shot_stream"
)

// ClaudeUsageLoadCriteria selects one host-owned Claude transcript.
type ClaudeUsageLoadCriteria struct {
	SessionID types.SessionID
}

// ClaudeUsageCounters preserves provider field presence. Nil is unavailable;
// a pointer to zero is a provider-reported known zero.
type ClaudeUsageCounters struct {
	InputTokens           *int64
	CachedInputTokens     *int64
	CacheWriteInputTokens *int64
	OutputTokens          *int64
	ReasoningOutputTokens *int64
	TotalTokens           *int64
}

// ClaudeUsageSample is one body-free authoritative call or run record.
type ClaudeUsageSample struct {
	RecordID      string
	SourceName    string
	SourceVersion string
	Model         string
	Scope         types.UsageScope
	ObservedAt    time.Time
	TerminalCode  types.UsageTerminalCode
	Available     bool
	Counters      ClaudeUsageCounters
}

// ClaudeUsageLoadResult contains the selected representation and any retained
// legacy evidence. Empty is a supported explicit outcome.
type ClaudeUsageLoadResult struct {
	Mode             ClaudeUsageCaptureMode
	Samples          []ClaudeUsageSample
	BoundaryObserved bool
}

// ClaudeUsageRepository persists provider-neutral observations idempotently.
type ClaudeUsageRepository interface {
	model.UsageObservationRepository
}

// ClaudeUsageSource reads only body-free usage metadata from local Claude files.
type ClaudeUsageSource interface {
	Load(context.Context, ClaudeUsageLoadCriteria) (ClaudeUsageLoadResult, error)
}

// ClaudeHeadlessUsageStream forwards a Traceary-owned Claude JSON stream while
// retaining only terminal body-free usage metadata.
type ClaudeHeadlessUsageStream interface {
	io.Writer
	Complete() (ClaudeUsageLoadResult, error)
}

// ClaudeHeadlessUsageStreamFactory creates one bounded stream adapter per run.
type ClaudeHeadlessUsageStreamFactory interface {
	New(io.Writer) ClaudeHeadlessUsageStream
}
