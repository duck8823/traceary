package usecase

import (
	"context"
	"errors"
	"golang.org/x/xerrors"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/types"
)

// ErrInvalidPayloadRehearsalConfig indicates missing mandatory safety bounds.
var ErrInvalidPayloadRehearsalConfig = errors.New("payload rehearsal requires an explicit copied target, live store, and positive resource caps")

// ErrUnsafePayloadRehearsalTransition rejects backend state/readiness drift.
var ErrUnsafePayloadRehearsalTransition = errors.New("payload rehearsal backend returned an unsafe workflow transition")

// PayloadRehearsalUsecase separates preview from every mutating operation.
type PayloadRehearsalUsecase interface {
	Preview(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Run(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Resume(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Scrub(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
	Rollback(context.Context, types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error)
}

type payloadRehearsalUsecase struct {
	preview  application.PayloadRehearsalPreview
	workflow application.PayloadRehearsalRunWorkflow
	runner   application.PayloadRehearsalRunner
}

// NewPayloadRehearsalUsecase constructs the copied-store rehearsal orchestrator.
func NewPayloadRehearsalUsecase(p application.PayloadRehearsalPreview, w application.PayloadRehearsalRunWorkflow, r application.PayloadRehearsalRunner) PayloadRehearsalUsecase {
	return &payloadRehearsalUsecase{preview: p, workflow: w, runner: r}
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
	if result.State != "planned" || !result.DryRunZeroWrite || !result.LiveIdentityOnly || result.ActivationReadiness.ActivationAllowed {
		return types.PayloadRehearsalMetrics{}, ErrUnsafePayloadRehearsalTransition
	}
	return result, nil
}
func (u *payloadRehearsalUsecase) Run(ctx context.Context, c types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error) {
	if err := validateRehearsal(c); err != nil {
		return types.PayloadRehearsalMetrics{}, err
	}
	preflight, err := u.Preview(ctx, c)
	if err != nil {
		return types.PayloadRehearsalMetrics{}, err
	}
	if preflight.FreeBytes < preflight.EstimatedHeadroom {
		return types.PayloadRehearsalMetrics{}, ErrUnsafePayloadRehearsalTransition
	}
	return u.run(ctx, c, types.PayloadRehearsalRunCommand{Mode: types.PayloadRehearsalStart})
}
func (u *payloadRehearsalUsecase) Resume(ctx context.Context, c types.PayloadRehearsalConfig) (types.PayloadRehearsalMetrics, error) {
	if err := validateRehearsal(c); err != nil {
		return types.PayloadRehearsalMetrics{}, err
	}
	return u.run(ctx, c, types.PayloadRehearsalRunCommand{Mode: types.PayloadRehearsalResume})
}

func (u *payloadRehearsalUsecase) run(ctx context.Context, c types.PayloadRehearsalConfig, command types.PayloadRehearsalRunCommand) (result types.PayloadRehearsalMetrics, resultErr error) {
	handle, result, err := u.workflow.Prepare(ctx, c, command)
	if err != nil {
		return types.PayloadRehearsalMetrics{}, xerrors.Errorf("prepare payload rehearsal: %w", err)
	}
	defer func() {
		if closeErr := u.workflow.Close(handle); resultErr == nil && closeErr != nil {
			resultErr = xerrors.Errorf("close payload rehearsal: %w", closeErr)
		}
	}()
	deadline := time.Now().Add(c.WallTimeLimit)
	for _, field := range types.OrderedPayloadRehearsalFields() {
		for time.Now().Before(deadline) {
			var done bool
			result, done, err = u.workflow.AdvanceField(ctx, handle, field)
			if err != nil {
				_, _ = u.workflow.Pause(ctx, handle)
				return result, xerrors.Errorf("advance payload rehearsal: %w", err)
			}
			if done {
				break
			}
		}
		if !time.Now().Before(deadline) {
			result, err = u.workflow.Pause(ctx, handle)
			if err != nil {
				return result, xerrors.Errorf("pause payload rehearsal: %w", err)
			}
			return result, nil
		}
	}
	result, err = u.workflow.Complete(ctx, handle)
	if err != nil {
		return result, xerrors.Errorf("complete payload rehearsal: %w", err)
	}
	if result.State != "completed" {
		return types.PayloadRehearsalMetrics{}, ErrUnsafePayloadRehearsalTransition
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
	if result.State != "scrubbed" || !result.ActivationReadiness.ScrubPassed || result.ActivationReadiness.ActivationAllowed {
		return types.PayloadRehearsalMetrics{}, ErrUnsafePayloadRehearsalTransition
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
	if result.State != "rolled_back" || !result.RollbackVerified || result.ActivationReadiness.ActivationAllowed {
		return types.PayloadRehearsalMetrics{}, ErrUnsafePayloadRehearsalTransition
	}
	return result, nil
}
