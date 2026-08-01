package usecase

import (
	"context"
	"errors"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/types"
)

// ErrInvalidPayloadRehearsalConfig indicates missing mandatory safety bounds.
var ErrInvalidPayloadRehearsalConfig = errors.New("payload rehearsal requires an explicit copied target, live store, and positive resource caps")

// PayloadRehearsalUsecase separates preview from every mutating operation.
type PayloadRehearsalUsecase interface {
	Preview(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Run(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Resume(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Scrub(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Rollback(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
}

type payloadRehearsalUsecase struct {
	preview application.PayloadRehearsalPreview
	runner  application.PayloadRehearsalRunner
}

// NewPayloadRehearsalUsecase constructs the copied-store rehearsal orchestrator.
func NewPayloadRehearsalUsecase(p application.PayloadRehearsalPreview, r application.PayloadRehearsalRunner) PayloadRehearsalUsecase {
	return &payloadRehearsalUsecase{preview: p, runner: r}
}

func validateRehearsal(c types.PayloadRehearsalConfig) error {
	if !c.Valid() {
		return ErrInvalidPayloadRehearsalConfig
	}
	return nil
}
func (u *payloadRehearsalUsecase) Preview(ctx context.Context, c types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error) {
	if err := validateRehearsal(c); err != nil {
		return types.PayloadRehearsalMetrics{}, err
	}
	result, err := u.preview.Preview(ctx, c)
	if err != nil {
		return types.PayloadRehearsalMetrics{}, xerrors.Errorf("preview payload rehearsal: %w", err)
	}
	return result, nil
}
func (u *payloadRehearsalUsecase) Run(ctx context.Context, c types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error) {
	if err := validateRehearsal(c); err != nil {
		return types.PayloadRehearsalMetrics{}, err
	}
	result, err := u.runner.Run(ctx, c, false)
	if err != nil {
		return types.PayloadRehearsalMetrics{}, xerrors.Errorf("run payload rehearsal: %w", err)
	}
	return result, nil
}
func (u *payloadRehearsalUsecase) Resume(ctx context.Context, c types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error) {
	if err := validateRehearsal(c); err != nil {
		return types.PayloadRehearsalMetrics{}, err
	}
	result, err := u.runner.Run(ctx, c, true)
	if err != nil {
		return types.PayloadRehearsalMetrics{}, xerrors.Errorf("resume payload rehearsal: %w", err)
	}
	return result, nil
}
func (u *payloadRehearsalUsecase) Scrub(ctx context.Context, c types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error) {
	if err := validateRehearsal(c); err != nil {
		return types.PayloadRehearsalMetrics{}, err
	}
	result, err := u.runner.Scrub(ctx, c)
	if err != nil {
		return types.PayloadRehearsalMetrics{}, xerrors.Errorf("scrub payload rehearsal: %w", err)
	}
	return result, nil
}
func (u *payloadRehearsalUsecase) Rollback(ctx context.Context, c types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error) {
	if err := validateRehearsal(c); err != nil {
		return types.PayloadRehearsalMetrics{}, err
	}
	result, err := u.runner.Rollback(ctx, c)
	if err != nil {
		return types.PayloadRehearsalMetrics{}, xerrors.Errorf("rollback payload rehearsal: %w", err)
	}
	return result, nil
}
