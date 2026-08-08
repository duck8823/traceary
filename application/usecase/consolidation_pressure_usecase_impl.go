package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type consolidationPressureUsecase struct {
	pressure   model.SessionConsolidationPressureRepository
	refinement model.SessionRefinementRepository
}

// NewConsolidationPressureUsecase creates the read-only stop-hook pressure check.
func NewConsolidationPressureUsecase(
	pressure model.SessionConsolidationPressureRepository,
	refinement model.SessionRefinementRepository,
) ConsolidationPressureUsecase {
	return &consolidationPressureUsecase{
		pressure:   pressure,
		refinement: refinement,
	}
}

func (u *consolidationPressureUsecase) Check(
	ctx context.Context,
	sessionID types.SessionID,
	thresholdBytes int64,
) (ConsolidationPressureResult, error) {
	if u.pressure == nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("session consolidation pressure repository is not configured")
	}
	if u.refinement == nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("session refinement repository is not configured")
	}
	if strings.TrimSpace(sessionID.String()) == "" {
		return ConsolidationPressureResult{}, xerrors.Errorf("session id is required")
	}

	coversTo := types.None[types.EventID]()
	previousSummary := types.None[string]()
	previousCoversTo := types.None[types.EventID]()

	existing, err := u.refinement.FindBySessionID(ctx, sessionID)
	if err != nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("failed to load session refinement: %w", err)
	}
	if row, ok := existing.Value(); ok && row != nil {
		coversTo = types.Some(row.CoversToEventID())
		previousSummary = types.Some(row.Summary())
		previousCoversTo = types.Some(row.CoversToEventID())
	}

	pressure, err := u.pressure.SumBodyBytesAfter(ctx, sessionID, coversTo)
	if err != nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("failed to measure consolidation pressure: %w", err)
	}

	due := thresholdBytes > 0 && pressure >= thresholdBytes
	return ConsolidationPressureResult{
		PressureBytes:    pressure,
		Due:              due,
		PreviousSummary:  previousSummary,
		PreviousCoversTo: previousCoversTo,
	}, nil
}
