package application

import (
	"context"
	"io"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// AntigravityUsageSnapshot contains the allowlisted cumulative fields from one
// idle status-line payload.
type AntigravityUsageSnapshot struct {
	ConversationID types.SessionID
	Model          string
	SourceVersion  string
	ObservedAt     time.Time
	InputTokens    int64
	OutputTokens   int64
}

// AntigravityUsageSource decodes only body-free, allowlisted status-line
// metadata. A nil snapshot is the supported result for non-idle states.
type AntigravityUsageSource interface {
	Decode(context.Context, io.Reader) (*AntigravityUsageSnapshot, error)
}

// AntigravityUsageRepository persists observations and exposes the current
// immutable snapshot head without leaking SQL details into the use case.
type AntigravityUsageRepository interface {
	model.UsageObservationRepository
	FindSnapshotHead(context.Context, string) (types.Optional[*model.UsageObservation], error)
}
