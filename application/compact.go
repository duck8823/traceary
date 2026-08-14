package application

import (
	"context"
	"fmt"
	"time"

	"github.com/duck8823/traceary/domain"
)

// CompactInput is the single-command compaction request.
type CompactInput struct {
	Source   string
	Force    bool
	KeepDays int
	Now      time.Time
}

// CompactResult is the operator-visible outcome of one rewrite.
type CompactResult struct {
	Run                 domain.CompactionRun
	BytesBefore         int64
	BytesAfter          int64
	UnrefinedRemaining  int
	UnrefinedBytes      int64
	MechanicalSummaries bool
}

// CompactFilter configures the copy-filter inside Build.
// A zero value is vacuum-only: no body discard, no AfterClone.
type CompactFilter struct {
	Cutoff     time.Time
	AfterClone func(context.Context, string) error
}

// BodyGate classifies discardable-age transcript bodies on the source.
type BodyGate struct {
	CoveredCount      int
	CoveredBytes      int64
	UnrefinedSessions int
	UnrefinedBytes    int64
}

// MustRefuse is true when compact would rewrite nothing that a fold authorized
// while unrefined discardable-age material still exists. Partial folds must
// not take this path: CoveredCount > 0 proceeds and leaves the rest.
func (g BodyGate) MustRefuse(force bool) bool {
	return !force && g.CoveredCount == 0 && g.UnrefinedSessions > 0
}

// DefaultCompactKeepDays matches the retired store gc window.
const DefaultCompactKeepDays = 90

// SessionRefineSkillName is the agent-facing fold mechanism named in the
// unrefined-material error. The CLI command is mentioned as a fallback.
const SessionRefineSkillName = "traceary-session-refine"

// UnrefinedMaterialError stops compact when every discardable-age body still
// needs a fold (or --force). It is the only refusal that is a policy stop
// rather than a protocol failure.
type UnrefinedMaterialError struct {
	Sessions int
	Bytes    int64
}

func (e UnrefinedMaterialError) Error() string {
	return fmt.Sprintf(
		"%d sessions have no refinement (%s of material).\n"+
			"Fold them with the %s skill (or `traceary session refine`), oldest first.\n"+
			"Compacting after folding the oldest sessions reclaims what those sessions authorize.\n"+
			"--force writes mechanical summaries for them: when / what kinds / how often /\n"+
			"which commands are kept. The agent's reasoning (why) is not recovered and is\n"+
			"gone for those ranges.",
		e.Sessions,
		formatCompactBytes(e.Bytes),
		SessionRefineSkillName,
	)
}

func formatCompactBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const (
		kiB = 1024
		miB = 1024 * kiB
		giB = 1024 * miB
	)
	switch {
	case n >= giB:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(giB))
	case n >= miB:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(miB))
	case n >= kiB:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kiB))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// ForceCoverMustComplete refuses --force when mechanical cover did not finish.
// HasMore is leftover discovery; Skipped is Failures.Count(). Either means
// unrefined material may still be on the work copy, so Compact must not
// report UnrefinedRemaining=0.
func ForceCoverMustComplete(hasMore bool, skipped int) error {
	if hasMore {
		return fmt.Errorf("compact --force cover is incomplete: more orphan ranges remain")
	}
	if skipped > 0 {
		return fmt.Errorf("compact --force cover is incomplete: %d orphan range(s) were skipped", skipped)
	}
	return nil
}

// CompactCutoff returns now minus keepDays, using DefaultCompactKeepDays when
// keepDays is not positive.
func CompactCutoff(now time.Time, keepDays int) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	if keepDays <= 0 {
		keepDays = DefaultCompactKeepDays
	}
	return now.UTC().AddDate(0, 0, -keepDays)
}
