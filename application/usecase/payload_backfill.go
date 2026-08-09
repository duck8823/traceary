package usecase

import (
	"context"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/types"
)

// ErrInvalidPayloadBackfillConfig indicates missing or unbounded batch limits.
var ErrInvalidPayloadBackfillConfig = errors.New("payload backfill requires a positive batch row limit within the hard cap")

// PayloadBackfillUsecase is the operator-facing live-store rewrite workflow.
type PayloadBackfillUsecase interface {
	Preview(context.Context, types.PayloadBackfillConfig) (types.PayloadBackfillResult, error)
	Run(context.Context, types.PayloadBackfillConfig) (types.PayloadBackfillResult, error)
	Resume(context.Context, types.PayloadBackfillConfig) (types.PayloadBackfillResult, error)
	Status(context.Context) (types.PayloadBackfillResult, error)
}

type payloadBackfillUsecase struct {
	backend application.PayloadBackfill
}

// NewPayloadBackfillUsecase constructs the live-store backfill orchestrator.
func NewPayloadBackfillUsecase(backend application.PayloadBackfill) PayloadBackfillUsecase {
	return &payloadBackfillUsecase{backend: backend}
}

func validateBackfill(c types.PayloadBackfillConfig) error {
	if !c.Valid() {
		return ErrInvalidPayloadBackfillConfig
	}
	return nil
}

func (u *payloadBackfillUsecase) Preview(ctx context.Context, c types.PayloadBackfillConfig) (types.PayloadBackfillResult, error) {
	if err := validateBackfill(c); err != nil {
		return types.PayloadBackfillResult{}, err
	}
	result, err := u.backend.Preview(ctx, c)
	if err != nil {
		return types.PayloadBackfillResult{}, xerrors.Errorf("preview payload backfill: %w", err)
	}
	return result, nil
}

func (u *payloadBackfillUsecase) Run(ctx context.Context, c types.PayloadBackfillConfig) (types.PayloadBackfillResult, error) {
	if err := validateBackfill(c); err != nil {
		return types.PayloadBackfillResult{}, err
	}
	result, err := u.backend.Run(ctx, c)
	if err != nil {
		return types.PayloadBackfillResult{}, xerrors.Errorf("run payload backfill: %w", err)
	}
	return result, nil
}

func (u *payloadBackfillUsecase) Resume(ctx context.Context, c types.PayloadBackfillConfig) (types.PayloadBackfillResult, error) {
	if err := validateBackfill(c); err != nil {
		return types.PayloadBackfillResult{}, err
	}
	result, err := u.backend.Resume(ctx, c)
	if err != nil {
		return types.PayloadBackfillResult{}, xerrors.Errorf("resume payload backfill: %w", err)
	}
	return result, nil
}

func (u *payloadBackfillUsecase) Status(ctx context.Context) (types.PayloadBackfillResult, error) {
	result, err := u.backend.Status(ctx)
	if err != nil {
		return types.PayloadBackfillResult{}, xerrors.Errorf("status payload backfill: %w", err)
	}
	return result, nil
}
