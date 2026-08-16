package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type sessionOrphanRangeUsecase struct {
	orphanRepo     model.SessionOrphanRangeRepository
	refinementRepo model.SessionRefinementRepository
	eventOrder     model.SessionEventOrderRepository
	clock          types.Clock
}

// NewSessionOrphanRangeUsecase creates the compact-boundary orphan recorder.
// clock may be nil; SystemClock is used in that case.
func NewSessionOrphanRangeUsecase(
	orphanRepo model.SessionOrphanRangeRepository,
	refinementRepo model.SessionRefinementRepository,
	eventOrder model.SessionEventOrderRepository,
	clock types.Clock,
) SessionOrphanRangeUsecase {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &sessionOrphanRangeUsecase{
		orphanRepo:     orphanRepo,
		refinementRepo: refinementRepo,
		eventOrder:     eventOrder,
		clock:          clock,
	}
}

func (u *sessionOrphanRangeUsecase) RecordAtCompact(
	ctx context.Context,
	sessionID types.SessionID,
	compactEventID types.EventID,
) error {
	if u.orphanRepo == nil {
		return xerrors.Errorf("session orphan range repository is not configured")
	}
	if u.refinementRepo == nil {
		return xerrors.Errorf("session refinement repository is not configured")
	}
	if u.eventOrder == nil {
		return xerrors.Errorf("session event order repository is not configured")
	}

	sessionID, err := types.SessionIDFrom(strings.TrimSpace(sessionID.String()))
	if err != nil {
		return xerrors.Errorf("failed to record orphan at compact: %w", err)
	}
	compactEventID, err = types.EventIDFrom(strings.TrimSpace(compactEventID.String()))
	if err != nil {
		return xerrors.Errorf("failed to record orphan at compact: %w", err)
	}

	eventSession, err := u.eventOrder.FindEventSessionID(ctx, compactEventID)
	if err != nil {
		return xerrors.Errorf("failed to look up compact event: %w", err)
	}
	ownedBy, ok := eventSession.Value()
	if !ok {
		return xerrors.Errorf("compact event %s not found: %w", compactEventID, model.ErrInvalidSessionOrphanRange)
	}
	if ownedBy != sessionID {
		return xerrors.Errorf(
			"compact event %s belongs to session %s, not %s: %w",
			compactEventID, ownedBy, sessionID, model.ErrInvalidSessionOrphanRange,
		)
	}

	current, err := u.refinementRepo.FindBySessionID(ctx, sessionID)
	if err != nil {
		return xerrors.Errorf("failed to load session refinement: %w", err)
	}

	from := types.None[types.EventID]()
	if existing, present := current.Value(); present {
		// When the refinement already covers the compact event (Claude digest
		// just landed), record nothing.
		coversTo := existing.CoversToEventID()
		if coversTo == compactEventID {
			return nil
		}
		after, err := u.eventOrder.EventIsStrictlyAfter(ctx, compactEventID, coversTo)
		if err != nil {
			return xerrors.Errorf("failed to compare coverage with compact event: %w", err)
		}
		if !after {
			// covers_to is at or after the compact event — nothing orphaned.
			return nil
		}
		// Exclusive lower bound is the covered bound.
		from = types.Some(coversTo)
	}

	// earliestEventTime is not persisted by Record (see insert_session_orphan_range.sql);
	// it only matters for objects built during discovery, so a filler value is
	// fine here.
	now := u.clock.Now()
	orphan, err := model.NewSessionOrphanRange(sessionID, from, compactEventID, now, now)
	if err != nil {
		return xerrors.Errorf("failed to build orphan range: %w", err)
	}
	if err := u.orphanRepo.Record(ctx, orphan); err != nil {
		return xerrors.Errorf("failed to record orphan range: %w", err)
	}
	return nil
}
