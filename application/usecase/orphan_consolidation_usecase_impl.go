package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// Mechanical summary target. Pathological ranges are truncated deterministically.
// The budget applies only to the gc-synthesised section — never to agent text
// that is preserved when composing onto an existing refinement.
const orphanMechanicalSummaryMaxBytes = 64 * 1024

// ProducedBy value for gc-authored degraded refinements.
const orphanConsolidationProducedBy = "gc:orphan-consolidation"

// defaultOrphanConsolidationLimit bounds discovery so a store with tens of
// thousands of unfolded sessions cannot turn one gc into an open-ended scan.
const defaultOrphanConsolidationLimit = 5000

// defaultOrphanConsolidationBudget bounds the processing loop. Each candidate
// costs a read-only connection, two range queries and a CAS write, so the pass
// is paced by wall clock rather than by a count that means nothing to an
// operator watching a command that has not returned.
const defaultOrphanConsolidationBudget = 2 * time.Minute

// orphanConsolidationConsecutiveFailureLimit distinguishes "this session's data
// is bad" from "the mechanism is broken". Isolated failures are skipped and
// counted; this many in a row means the next candidate will fail too, and
// continuing would report thousands of skips instead of one clear error.
const orphanConsolidationConsecutiveFailureLimit = 3

type orphanConsolidationUsecase struct {
	orphans    model.SessionOrphanRangeRepository
	refinement SessionRefinementUsecase
	clock      types.Clock
}

// NewOrphanConsolidationUsecase creates the orphan consolidation use case.
// clock may be nil; SystemClock is used in that case.
func NewOrphanConsolidationUsecase(
	orphans model.SessionOrphanRangeRepository,
	refinement SessionRefinementUsecase,
	clock types.Clock,
) OrphanConsolidationUsecase {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &orphanConsolidationUsecase{
		orphans:    orphans,
		refinement: refinement,
		clock:      clock,
	}
}

func (u *orphanConsolidationUsecase) Consolidate(
	ctx context.Context,
	input OrphanConsolidationInput,
) (apptypes.OrphanConsolidationResult, error) {
	if u.orphans == nil {
		return apptypes.OrphanConsolidationResult{}, xerrors.Errorf("session orphan range repository is not configured")
	}
	if u.refinement == nil {
		return apptypes.OrphanConsolidationResult{}, xerrors.Errorf("session refinement usecase is not configured")
	}
	if input.StaleAfter <= 0 {
		return apptypes.OrphanConsolidationResult{}, xerrors.Errorf("staleAfter must be greater than zero")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultOrphanConsolidationLimit
	}
	budget := input.Budget
	if budget <= 0 {
		budget = defaultOrphanConsolidationBudget
	}

	started := u.clock.Now()
	now := started
	candidates, err := u.orphans.DiscoverCandidates(ctx, input.StaleAfter, now, limit)
	if err != nil {
		return apptypes.OrphanConsolidationResult{}, xerrors.Errorf("failed to discover orphan ranges: %w", err)
	}

	hasMore := candidates.HasMore
	attempted := 0
	produced := 0
	failures := apptypes.OrphanConsolidationFailuresOf(nil)
	consecutive := 0

	for _, orphan := range candidates.Ranges {
		if orphan == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			// A cancelled or timed-out context is not a skippable candidate failure.
			return apptypes.OrphanConsolidationResult{}, xerrors.Errorf("orphan consolidation cancelled: %w", err)
		}
		if u.clock.Now().Sub(started) >= budget {
			hasMore = true
			break
		}

		attempted++
		wrote, failure := u.processCandidate(ctx, orphan, input.DryRun)
		if failure == nil {
			if wrote {
				produced++
			}
			consecutive = 0
			continue
		}
		failures = failures.Add(apptypes.OrphanConsolidationFailureOf(
			orphan.SessionID().String(), failure.Error(),
		))
		consecutive++
		slog.Warn(
			"skipped orphan range",
			"session_id", orphan.SessionID().String(),
			"dry_run", input.DryRun,
			"error", failure,
		)
		if consecutive >= orphanConsolidationConsecutiveFailureLimit {
			return apptypes.OrphanConsolidationResult{}, xerrors.Errorf(
				"orphan consolidation aborted after %d consecutive failures (last session %s): %w",
				consecutive, orphan.SessionID(), failure,
			)
		}
	}
	return apptypes.OrphanConsolidationResultOf(attempted, produced, failures, hasMore, input.DryRun), nil
}

// processCandidate performs the single operation one candidate needs: a dry run
// only proves the material is still readable, an apply writes the degraded
// refinement. Both fail the same way, so the caller keeps one skip rule instead
// of two copies of it.
func (u *orphanConsolidationUsecase) processCandidate(
	ctx context.Context,
	orphan *model.SessionOrphanRange,
	dryRun bool,
) (bool, error) {
	material, err := u.orphans.LoadMaterial(ctx, orphan.SessionID(), orphan.FromEventID(), orphan.ToEventID())
	if err != nil {
		if dryRun {
			return false, xerrors.Errorf("failed to load orphan material for dry-run session %s: %w", orphan.SessionID(), err)
		}
		return false, xerrors.Errorf("failed to load orphan material for session %s: %w", orphan.SessionID(), err)
	}
	if orphanRangeIsLifecycleOnly(material.KindCounts) {
		if dryRun {
			return true, nil
		}
		return u.advanceLifecycleCoverage(ctx, orphan)
	}
	if dryRun {
		return true, nil
	}
	return true, u.produceDegraded(ctx, orphan, material)
}

// advanceLifecycleCoverage extends covers_to over a start/end-only tail
// without synthesising a mechanical footnote. No existing fold means there
// is nothing to keep eligible, so the candidate is skipped.
func (u *orphanConsolidationUsecase) advanceLifecycleCoverage(
	ctx context.Context,
	orphan *model.SessionOrphanRange,
) (bool, error) {
	_, err := u.refinement.Refine(ctx, SessionRefineInput{
		SessionID:    orphan.SessionID(),
		ProducedBy:   orphanConsolidationProducedBy,
		CoversTo:     orphan.ToEventID(),
		CoverageOnly: true,
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errCoverageOnlyNoRow) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to advance lifecycle coverage for session %s: %w", orphan.SessionID(), err)
}

func (u *orphanConsolidationUsecase) produceDegraded(
	ctx context.Context,
	orphan *model.SessionOrphanRange,
	material model.SessionOrphanMaterial,
) error {
	mechanical := buildMechanicalOrphanSummary(material)

	// Composition runs inside Refine's CAS loop (ComposeSummary), not here.
	// A pre-read would freeze summary text across retries and could overwrite
	// agent prose that landed between the first read and a successful write.
	//
	// degraded=true even when the row still contains agent-authored text.
	// The flag means the stored summary is not purely agent-written: a mixed
	// row includes gc-synthesised prose, so consumers must not treat the whole
	// row as agent reasoning. Seam markers in the text separate authorships.
	_, err := u.refinement.Refine(ctx, SessionRefineInput{
		SessionID:  orphan.SessionID(),
		ProducedBy: orphanConsolidationProducedBy,
		CoversTo:   orphan.ToEventID(),
		Degraded:   true,
		ComposeSummary: func(current types.Optional[*model.SessionRefinement]) (string, string) {
			if existing, present := current.Value(); present {
				// Refine on supersede replaces summary text wholesale while
				// keeping covers_from. Composing preserves the agent-authored
				// prose and only appends a marked degraded section for the
				// orphan tail.
				return composeOrphanSummary(existing.Summary(), orphan, mechanical), existing.Keywords()
			}
			return mechanical, ""
		},
	})
	if err != nil {
		return xerrors.Errorf("failed to write degraded refinement for session %s: %w", orphan.SessionID(), err)
	}
	return nil
}

// composeOrphanSummary preserves an existing summary and appends a clearly
// marked mechanical section for the orphan tail only. Truncation of the
// mechanical section is applied before this call; agent text is never cut.
func composeOrphanSummary(existingSummary string, orphan *model.SessionOrphanRange, mechanical string) string {
	var b strings.Builder
	b.WriteString(existingSummary)
	b.WriteString("\n\n---\n")
	rangeDesc := orphanRangeCoverageDesc(orphan)
	fmt.Fprintf(
		&b,
		"[degraded section: %s, synthesised by %s]\n",
		rangeDesc,
		orphanConsolidationProducedBy,
	)
	b.WriteString(mechanical)
	return b.String()
}

// orphanRangeCoverageDesc names the event range the mechanical section covers
// so a reader can match text to coverage without inferring from context.
func orphanRangeCoverageDesc(orphan *model.SessionOrphanRange) string {
	if from, ok := orphan.FromEventID().Value(); ok {
		return fmt.Sprintf(
			"orphan events after %s through %s (agent text above is unchanged)",
			from.String(),
			orphan.ToEventID().String(),
		)
	}
	return fmt.Sprintf(
		"orphan events from session start through %s",
		orphan.ToEventID().String(),
	)
}

// buildMechanicalOrphanSummary produces an honest LLM-free summary: when, what
// kinds, how often, which commands. It does not recover agent reasoning.
// Truncation is deterministic and applies only to this synthesised text.
func buildMechanicalOrphanSummary(material model.SessionOrphanMaterial) string {
	var b strings.Builder
	b.WriteString("Mechanical summary (degraded=1).\n")
	b.WriteString("This recovers when events occurred, which kinds appeared and how often, and which commands ran.\n")
	b.WriteString("It does not recover agent reasoning (why); that was not folded and is gone for this range.\n\n")

	if !material.FirstCreatedAt.IsZero() && !material.LastCreatedAt.IsZero() {
		fmt.Fprintf(
			&b,
			"Time span: %s .. %s\n",
			material.FirstCreatedAt.UTC().Format(time.RFC3339Nano),
			material.LastCreatedAt.UTC().Format(time.RFC3339Nano),
		)
	}
	fmt.Fprintf(&b, "Events: %d total\n", material.EventCount)

	kinds := make([]string, 0, len(material.KindCounts))
	for kind := range material.KindCounts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		fmt.Fprintf(&b, "  %s: %d\n", kind, material.KindCounts[kind])
	}

	if len(material.Commands) == 0 {
		b.WriteString("Commands: (none)\n")
	} else {
		b.WriteString("Commands:\n")
		for _, command := range material.Commands {
			fmt.Fprintf(&b, "  %s\n", command)
		}
	}

	text := b.String()
	if len(text) <= orphanMechanicalSummaryMaxBytes {
		return text
	}
	// Truncate on a rune boundary and declare the truncation honestly.
	const marker = "\n[truncated: mechanical summary exceeded 64 KiB]\n"
	limit := orphanMechanicalSummaryMaxBytes - len(marker)
	if limit < 1 {
		return marker
	}
	truncated := text[:limit]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + marker
}

// orphanRangeIsLifecycleOnly is true when every kind in the range is a session
// boundary. Those tails carry no agent reasoning, so synthesising a "why is
// gone" footnote is both untrue and destructive (#1877).
func orphanRangeIsLifecycleOnly(kindCounts map[string]int) bool {
	saw := false
	for kind, count := range kindCounts {
		if count <= 0 {
			continue
		}
		saw = true
		if kind != string(types.EventKindSessionStarted) && kind != string(types.EventKindSessionEnded) {
			return false
		}
	}
	return saw
}
