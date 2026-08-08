package usecase

import (
	"context"
	"fmt"
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

	now := u.clock.Now()
	candidates, err := u.orphans.DiscoverCandidates(ctx, input.StaleAfter, now)
	if err != nil {
		return apptypes.OrphanConsolidationResult{}, xerrors.Errorf("failed to discover orphan ranges: %w", err)
	}

	produced := 0
	for _, orphan := range candidates {
		if orphan == nil {
			continue
		}
		if input.DryRun {
			// Dry-run must not write refinements. Count ranges that still have
			// material; LoadMaterial failing closed is reported as an error so
			// operators do not get a silent zero.
			if _, err := u.orphans.LoadMaterial(ctx, orphan.SessionID(), orphan.FromEventID(), orphan.ToEventID()); err != nil {
				return apptypes.OrphanConsolidationResult{}, xerrors.Errorf(
					"failed to load orphan material for dry-run session %s: %w",
					orphan.SessionID(), err,
				)
			}
			produced++
			continue
		}
		if err := u.produceDegraded(ctx, orphan); err != nil {
			return apptypes.OrphanConsolidationResult{}, err
		}
		produced++
	}
	return apptypes.OrphanConsolidationResultOf(produced, input.DryRun), nil
}

func (u *orphanConsolidationUsecase) produceDegraded(ctx context.Context, orphan *model.SessionOrphanRange) error {
	material, err := u.orphans.LoadMaterial(ctx, orphan.SessionID(), orphan.FromEventID(), orphan.ToEventID())
	if err != nil {
		return xerrors.Errorf("failed to load orphan material for session %s: %w", orphan.SessionID(), err)
	}
	mechanical := buildMechanicalOrphanSummary(material)

	// Composition runs inside Refine's CAS loop (ComposeSummary), not here.
	// A pre-read would freeze summary text across retries and could overwrite
	// agent prose that landed between the first read and a successful write.
	//
	// degraded=true even when the row still contains agent-authored text.
	// The flag means the stored summary is not purely agent-written: a mixed
	// row includes gc-synthesised prose, so consumers must not treat the whole
	// row as agent reasoning. Seam markers in the text separate authorships.
	_, err = u.refinement.Refine(ctx, SessionRefineInput{
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
