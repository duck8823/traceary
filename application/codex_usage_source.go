package application

import (
	"context"
	"time"

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

// CodexUsageSample is one body-free, authoritative completed-call record.
type CodexUsageSample struct {
	RecordID      string
	SourceName    string
	SourceVersion string
	Model         string
	ObservedAt    time.Time
	Counters      CodexUsageCounters
}

// CodexUsageLoadResult contains every verified completed call currently
// visible in the session source. Empty is a supported explicit outcome.
type CodexUsageLoadResult struct {
	Samples []CodexUsageSample
}

// CodexUsageSource reads only body-free usage metadata from local Codex files.
type CodexUsageSource interface {
	Load(ctx context.Context, criteria CodexUsageLoadCriteria) (CodexUsageLoadResult, error)
}
