package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const sessionRefineMaxAttempts = 3

// errCoverageOnlyNoRow is returned when CoverageOnly is set and the session
// has no refinement to extend. Orphan consolidation treats it as a skip,
// not a candidate failure.
var errCoverageOnlyNoRow = xerrors.New("coverage-only refine requires an existing session refinement")

type sessionRefinementUsecase struct {
	sessionRepo    model.SessionRepository
	refinementRepo model.SessionRefinementRepository
	eventOrder     model.SessionEventOrderRepository
	clock          types.Clock
}

// NewSessionRefinementUsecase creates the L2 refinement write port.
// clock may be nil; SystemClock is used in that case.
func NewSessionRefinementUsecase(
	sessionRepo model.SessionRepository,
	refinementRepo model.SessionRefinementRepository,
	eventOrder model.SessionEventOrderRepository,
	clock types.Clock,
) SessionRefinementUsecase {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &sessionRefinementUsecase{
		sessionRepo:    sessionRepo,
		refinementRepo: refinementRepo,
		eventOrder:     eventOrder,
		clock:          clock,
	}
}

func (u *sessionRefinementUsecase) Refine(ctx context.Context, input SessionRefineInput) (model.SessionRefineResult, error) {
	if u.sessionRepo == nil {
		return model.SessionRefineResult{}, xerrors.Errorf("session repository is not configured")
	}
	if u.refinementRepo == nil {
		return model.SessionRefineResult{}, xerrors.Errorf("session refinement repository is not configured")
	}
	if u.eventOrder == nil {
		return model.SessionRefineResult{}, xerrors.Errorf("session event order repository is not configured")
	}

	sessionID, err := types.SessionIDFrom(strings.TrimSpace(input.SessionID.String()))
	if err != nil {
		return model.SessionRefineResult{}, xerrors.Errorf("failed to refine session: %w", err)
	}
	coversTo, err := types.EventIDFrom(strings.TrimSpace(input.CoversTo.String()))
	if err != nil {
		return model.SessionRefineResult{}, xerrors.Errorf("failed to refine session: %w", err)
	}
	producedBy := strings.TrimSpace(input.ProducedBy)
	if producedBy == "" {
		return model.SessionRefineResult{}, xerrors.Errorf("produced_by is required: %w", model.ErrInvalidSessionRefinement)
	}

	existingSession, err := u.sessionRepo.FindByID(ctx, sessionID)
	if err != nil {
		return model.SessionRefineResult{}, xerrors.Errorf("failed to look up session: %w", err)
	}
	if _, ok := existingSession.Value(); !ok {
		return model.SessionRefineResult{}, xerrors.Errorf("session %s not found: %w", sessionID, model.ErrInvalidSessionState)
	}

	eventSession, err := u.eventOrder.FindEventSessionID(ctx, coversTo)
	if err != nil {
		return model.SessionRefineResult{}, xerrors.Errorf("failed to look up covers_to event: %w", err)
	}
	ownedBy, ok := eventSession.Value()
	if !ok {
		return model.SessionRefineResult{}, xerrors.Errorf("covers_to event %s not found: %w", coversTo, model.ErrInvalidSessionRefinement)
	}
	if ownedBy != sessionID {
		return model.SessionRefineResult{}, xerrors.Errorf(
			"covers_to event %s belongs to session %s, not %s: %w",
			coversTo, ownedBy, sessionID, model.ErrInvalidSessionRefinement,
		)
	}

	for attempt := 0; attempt < sessionRefineMaxAttempts; attempt++ {
		result, written, err := u.decideAndWrite(ctx, input, sessionID, coversTo, producedBy)
		if err != nil {
			return model.SessionRefineResult{}, err
		}
		if written {
			return result, nil
		}
		// Lost the compare-and-swap race: re-read and decide again.
	}

	return model.SessionRefineResult{}, xerrors.Errorf(
		"failed to refine session %s after %d concurrent attempts",
		sessionID, sessionRefineMaxAttempts,
	)
}

// decideAndWrite reads the current row, decides created/superseded/unchanged,
// and attempts a compare-and-swap write. written is false when another writer
// won the race (caller should re-read). Unchanged outcomes set written true
// because no write is required.
//
// Summary text is resolved after the re-read: ComposeSummary (when set) sees
// the same row the write is conditioned on, so a lost race cannot freeze
// superseded prose into a later successful write.
func (u *sessionRefinementUsecase) decideAndWrite(
	ctx context.Context,
	input SessionRefineInput,
	sessionID types.SessionID,
	coversTo types.EventID,
	producedBy string,
) (model.SessionRefineResult, bool, error) {
	current, err := u.refinementRepo.FindBySessionID(ctx, sessionID)
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to load session refinement: %w", err)
	}
	if input.CoverageOnly {
		return u.advanceCoverageOnly(ctx, current, sessionID, coversTo)
	}
	summary, keywords := resolveRefineText(input, current)
	if existing, present := current.Value(); present {
		// Idempotency: only advance when covers_to is strictly after the stored
		// bound. Equal or earlier ranges must not bump generation or rewrite text.
		after, err := u.eventOrder.EventIsStrictlyAfter(ctx, coversTo, existing.CoversToEventID())
		if err != nil {
			return model.SessionRefineResult{}, false, xerrors.Errorf("failed to compare coverage: %w", err)
		}
		if !after {
			result, err := model.SessionRefineResultOf(model.SessionRefineOutcomeUnchanged, existing)
			if err != nil {
				return model.SessionRefineResult{}, false, xerrors.Errorf("failed to build unchanged refine result: %w", err)
			}
			return result, true, nil
		}
		// Coverage only advances: keep the earlier covers_from so the row
		// spans every event that has ever been summarised for this session.
		next, err := model.NewSessionRefinement(
			sessionID,
			existing.Generation()+1,
			existing.CoversFromEventID(),
			coversTo,
			summary,
			keywords,
			producedBy,
			u.clock.Now(),
			input.Degraded,
		)
		if err != nil {
			return model.SessionRefineResult{}, false, xerrors.Errorf("failed to build superseding refinement: %w", err)
		}
		next = next.WithHasAgentReasoning(refineHasAgentReasoning(input, current))
		advanced, err := u.refinementRepo.SaveIfAdvances(ctx, next, existing.Generation())
		if err != nil {
			return model.SessionRefineResult{}, false, xerrors.Errorf("failed to save session refinement: %w", err)
		}
		if !advanced {
			return model.SessionRefineResult{}, false, nil
		}
		result, err := model.SessionRefineResultOf(model.SessionRefineOutcomeSuperseded, next)
		if err != nil {
			return model.SessionRefineResult{}, false, xerrors.Errorf("failed to build superseded refine result: %w", err)
		}
		return result, true, nil
	}

	earliest, err := u.eventOrder.EarliestEventID(ctx, sessionID)
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to resolve covers_from: %w", err)
	}
	coversFrom, ok := earliest.Value()
	if !ok {
		return model.SessionRefineResult{}, false, xerrors.Errorf("session %s has no events: %w", sessionID, model.ErrInvalidSessionRefinement)
	}
	created, err := model.NewSessionRefinement(
		sessionID,
		1,
		coversFrom,
		coversTo,
		summary,
		keywords,
		producedBy,
		u.clock.Now(),
		input.Degraded,
	)
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to build session refinement: %w", err)
	}
	// First write keeps NewSessionRefinement's !Degraded default. Applying the
	// zero-value input flag here would mark every agent fold that omitted the
	// field as ineligible for wake.
	// expectedGeneration 0: expect no row yet (CHECK generation > 0 makes the
	// update branch unsatisfiable if a concurrent insert already landed).
	advanced, err := u.refinementRepo.SaveIfAdvances(ctx, created, 0)
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to save session refinement: %w", err)
	}
	if !advanced {
		return model.SessionRefineResult{}, false, nil
	}
	result, err := model.SessionRefineResultOf(model.SessionRefineOutcomeCreated, created)
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to build created refine result: %w", err)
	}
	return result, true, nil
}

// advanceCoverageOnly widens covers_to without rewriting stored text or flags.
func (u *sessionRefinementUsecase) advanceCoverageOnly(
	ctx context.Context,
	current types.Optional[*model.SessionRefinement],
	sessionID types.SessionID,
	coversTo types.EventID,
) (model.SessionRefineResult, bool, error) {
	existing, present := current.Value()
	if !present {
		return model.SessionRefineResult{}, false, errCoverageOnlyNoRow
	}
	after, err := u.eventOrder.EventIsStrictlyAfter(ctx, coversTo, existing.CoversToEventID())
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to compare coverage: %w", err)
	}
	if !after {
		result, err := model.SessionRefineResultOf(model.SessionRefineOutcomeUnchanged, existing)
		if err != nil {
			return model.SessionRefineResult{}, false, xerrors.Errorf("failed to build unchanged refine result: %w", err)
		}
		return result, true, nil
	}
	next, err := model.NewSessionRefinement(
		sessionID,
		existing.Generation()+1,
		existing.CoversFromEventID(),
		coversTo,
		existing.Summary(),
		existing.Keywords(),
		existing.ProducedBy(),
		u.clock.Now(),
		existing.Degraded(),
	)
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to build coverage-only refinement: %w", err)
	}
	next = next.WithHasAgentReasoning(existing.HasAgentReasoning())
	advanced, err := u.refinementRepo.SaveIfAdvances(ctx, next, existing.Generation())
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to save session refinement: %w", err)
	}
	if !advanced {
		return model.SessionRefineResult{}, false, nil
	}
	result, err := model.SessionRefineResultOf(model.SessionRefineOutcomeSuperseded, next)
	if err != nil {
		return model.SessionRefineResult{}, false, xerrors.Errorf("failed to build superseded refine result: %w", err)
	}
	return result, true, nil
}

// refineHasAgentReasoning keeps a stored 1 across orphan compose. First writes
// do not call this; they keep NewSessionRefinement's !Degraded default.
func refineHasAgentReasoning(input SessionRefineInput, current types.Optional[*model.SessionRefinement]) bool {
	if existing, present := current.Value(); present && existing.HasAgentReasoning() {
		return true
	}
	return input.HasAgentReasoning
}

// resolveRefineText returns the summary/keywords for this CAS attempt.
// ComposeSummary observes the just-read row; plain Summary/Keywords do not.
func resolveRefineText(
	input SessionRefineInput,
	current types.Optional[*model.SessionRefinement],
) (summary, keywords string) {
	if input.ComposeSummary != nil {
		return input.ComposeSummary(current)
	}
	return input.Summary, input.Keywords
}
