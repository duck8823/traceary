package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type consolidationRequestUsecase struct {
	repo  model.ConsolidationRequestRepository
	clock types.Clock
}

// NewConsolidationRequestUsecase wires the ledger write port.
func NewConsolidationRequestUsecase(
	repo model.ConsolidationRequestRepository,
	clock types.Clock,
) ConsolidationRequestUsecase {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &consolidationRequestUsecase{repo: repo, clock: clock}
}

func (u *consolidationRequestUsecase) Record(
	ctx context.Context,
	in ConsolidationRequestInput,
) (ConsolidationRequestRecorded, error) {
	open, err := u.repo.FindLatestOpen(ctx, in.SessionID)
	if err != nil {
		return ConsolidationRequestRecorded{}, xerrors.Errorf("failed to look up open consolidation request: %w", err)
	}
	_, reRequest := open.Value()
	request, err := model.NewConsolidationRequest(
		in.SessionID,
		in.Client,
		u.clock.Now(),
		in.AtEventID,
		in.Signal,
		in.PressureValue,
		in.ThresholdValue,
		reRequest,
		in.Delivery,
	)
	if err != nil {
		return ConsolidationRequestRecorded{}, xerrors.Errorf("invalid consolidation request: %w", err)
	}
	recorded, err := u.repo.Save(ctx, request)
	if err != nil {
		return ConsolidationRequestRecorded{}, xerrors.Errorf("failed to save consolidation request: %w", err)
	}
	return ConsolidationRequestRecorded{Recorded: recorded, ReRequest: reRequest}, nil
}

func (u *consolidationRequestUsecase) HasOpenRequest(
	ctx context.Context,
	sessionID types.SessionID,
) (bool, error) {
	open, err := u.repo.FindLatestOpen(ctx, sessionID)
	if err != nil {
		return false, xerrors.Errorf("failed to look up open consolidation request: %w", err)
	}
	_, ok := open.Value()
	return ok, nil
}

func (u *consolidationRequestUsecase) RecordRefineOutcome(
	ctx context.Context,
	stamp model.ConsolidationRefineStamp,
) (bool, error) {
	if stamp.At.IsZero() {
		stamp.At = u.clock.Now()
	}
	stamped, err := u.repo.MarkRefineOutcome(ctx, stamp)
	if err != nil {
		return false, xerrors.Errorf("failed to stamp consolidation request: %w", err)
	}
	return stamped, nil
}
