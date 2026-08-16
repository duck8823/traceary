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
	// WorkDir, when set, holds the copy-filter work file on another volume
	// so the source volume only needs dest-sized free space for VACUUM INTO.
	WorkDir string
}

// Compact strategies recorded in CompactResult.CompactStrategy.
const (
	CompactStrategyReplica   = "replica"
	CompactStrategyExternal  = "external"
	CompactStrategyDestSized = "dest_sized"
	CompactStrategyInPlace   = "in_place"
)

// CompactResult is the operator-visible outcome of one rewrite.
type CompactResult struct {
	Run                       domain.CompactionRun
	BytesBefore               int64
	BytesAfter                int64
	UnrefinedRemaining        int
	UnrefinedBytes            int64
	MechanicalSummaries       bool
	ReleasedCommandBodyRows   int
	ReleasedCommandBodyBytes  int64
	EstimatedReclaimableBytes int64
	CompactStrategy           string
}

// CommandBodyReclaim is the measured set of duplicated command_executed
// bodies compact will clear. Bytes are stored blob lengths, not plaintext.
type CommandBodyReclaim struct {
	Rows  int
	Bytes int64
}

// CompactFilter configures the copy-filter inside Build.
// A zero value is vacuum-only: no body discard, no AfterClone.
type CompactFilter struct {
	Cutoff time.Time
	// AfterClone runs the --force mechanical cover on the work copy before the
	// discard/vacuum steps. It receives Cutoff so the cover can decide it has
	// folded enough of the oldest backlog to let CollectGarbage proceed even
	// when unrelated, newer-than-cutoff material is still unfolded (#1721).
	AfterClone func(ctx context.Context, work string, cutoff time.Time) error
	// WorkDir stages the source-sized work copy on another volume (#2008).
	WorkDir string
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

// ForceCoverSafeToDelete refuses --force when mechanical cover left behind
// material that might still be older than cutoff. hasMore is leftover
// discovery; earliestUnprocessed is that leftover's earliest event time (nil
// when hasMore is false, or when the pass could not determine it). skipped is
// Failures.Count(); earliestSkipped is the same for skipped candidates.
//
// This replaces the earlier "cover must be 100% complete" gate (#1795's
// Complete(), later reconstructed as hasMore==false && skipped==0). That gate
// refused any leftover regardless of age, which meant a large but
// newer-than-cutoff backlog blocked deletion indefinitely (#1721). Discovery
// now orders candidates oldest-first, so the earliest leftover time is a
// sound bound: deletion is safe once every leftover range is no older than
// cutoff. An unknown time (nil while the corresponding count is nonzero)
// fails closed, matching the previous conservatism.
func ForceCoverSafeToDelete(
	hasMore bool, earliestUnprocessed *time.Time,
	skipped int, earliestSkipped *time.Time,
	cutoff time.Time,
) error {
	if hasMore {
		if earliestUnprocessed == nil {
			return fmt.Errorf("compact --force cover is incomplete: earliest unprocessed orphan range time is unknown")
		}
		if earliestUnprocessed.Before(cutoff) {
			return fmt.Errorf("compact --force cover is incomplete: unprocessed orphan ranges may be older than the retention cutoff")
		}
	}
	if skipped > 0 {
		if earliestSkipped == nil {
			return fmt.Errorf("compact --force cover is incomplete: earliest skipped orphan range time is unknown")
		}
		if earliestSkipped.Before(cutoff) {
			return fmt.Errorf("compact --force cover is incomplete: %d orphan range(s) were skipped and may be older than the retention cutoff", skipped)
		}
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
