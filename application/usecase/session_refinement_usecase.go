package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SessionRefineInput is the write-port request.
//
// Agent callers set Summary (and Keywords) to the finished text. When the text
// depends on the currently stored row (gc orphan composition), set
// ComposeSummary instead: decideAndWrite invokes it with the row just re-read
// on each compare-and-swap attempt so the summary written on attempt N is
// composed from the row observed on attempt N. When ComposeSummary is set,
// Summary and Keywords are ignored.
type SessionRefineInput struct {
	SessionID  types.SessionID
	Summary    string
	Keywords   string
	ProducedBy string
	CoversTo   types.EventID
	Degraded   bool
	// HasAgentReasoning is the first-write value only when Degraded is true
	// (unused by current callers). A non-degraded first write always stores 1.
	// On supersede, a stored 1 is preserved so orphan compose does not drop
	// wake eligibility (#1877).
	HasAgentReasoning bool
	// CoverageOnly advances covers_to while keeping stored summary text,
	// degraded, has_agent_reasoning, and produced_by. Lifecycle-only orphan
	// tails use this so a session_ended event does not attach a mechanical
	// footnote (#1877). With no existing row, Refine returns
	// errCoverageOnlyNoRow and inserts nothing.
	CoverageOnly bool
	// ComposeSummary derives summary and keywords from the current refinement
	// row on each CAS attempt. See type comment above.
	ComposeSummary func(current types.Optional[*model.SessionRefinement]) (summary, keywords string)
}

// SessionRefinementUsecase is the L2 refinement write port.
type SessionRefinementUsecase interface {
	// Refine stores or supersedes a session refinement. Replaying the same
	// covers_to range is a no-op: one row, unchanged generation and text.
	Refine(ctx context.Context, input SessionRefineInput) (model.SessionRefineResult, error)
}
