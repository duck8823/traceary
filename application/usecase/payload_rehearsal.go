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
	scrubber application.PayloadRehearsalScrubWorkflow
	runner   application.PayloadRehearsalRunner
}

func evaluated(result types.PayloadRehearsalMetrics) types.PayloadRehearsalMetrics {
	result.ActivationReadiness = application.EvaluatePayloadActivationReadiness(result)
	return result
}

func liveResult(result types.PayloadRehearsalMetrics) (types.PayloadRehearsalMetrics, error) {
	result = evaluated(result)
	if !result.LiveIdentityOnly {
		return result, ErrUnsafePayloadRehearsalTransition
	}
	return result, nil
}

// NewPayloadRehearsalUsecase constructs the copied-store rehearsal orchestrator.
func NewPayloadRehearsalUsecase(p application.PayloadRehearsalPreview, w application.PayloadRehearsalRunWorkflow, s application.PayloadRehearsalScrubWorkflow, r application.PayloadRehearsalRunner) PayloadRehearsalUsecase {
	return &payloadRehearsalUsecase{preview: p, workflow: w, scrubber: s, runner: r}
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
	result = evaluated(result)
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
		return result, xerrors.Errorf("prepare payload rehearsal: %w", err)
	}
	if result, err = liveResult(result); err != nil {
		_, _ = u.workflow.Pause(context.WithoutCancel(ctx), handle)
		_ = u.workflow.Close(handle)
		return result, err
	}
	defer func() {
		if closeErr := u.workflow.Close(handle); resultErr == nil && closeErr != nil {
			resultErr = xerrors.Errorf("close payload rehearsal: %w", closeErr)
		}
	}()
	deadline := time.Now().Add(c.WallTimeLimit)
	initialBatches := result.BatchCount
	fields := types.OrderedPayloadRehearsalFields()
	for fieldIndex, field := range fields {
		for time.Now().Before(deadline) {
			var done bool
			result, done, err = u.workflow.AdvanceField(ctx, handle, field)
			if err != nil {
				pauseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
				_, _ = u.workflow.Pause(pauseCtx, handle)
				cancel()
				return result, xerrors.Errorf("advance payload rehearsal: %w", err)
			}
			if result, err = liveResult(result); err != nil {
				_, _ = u.workflow.Pause(context.WithoutCancel(ctx), handle)
				return result, err
			}
			if c.StopAfterBatches > 0 && result.BatchCount-initialBatches >= c.StopAfterBatches && !(done && fieldIndex == len(fields)-1) {
				result, err = u.workflow.Pause(ctx, handle)
				if err != nil {
					return result, xerrors.Errorf("pause payload rehearsal at committed batch boundary: %w", err)
				}
				result.MorePending = true
				return liveResult(result)
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
			return liveResult(result)
		}
	}
	result, err = u.workflow.Complete(ctx, handle)
	if err != nil {
		return result, xerrors.Errorf("complete payload rehearsal: %w", err)
	}
	if result, err = liveResult(result); err != nil {
		return result, err
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
	handle, result, err := u.scrubber.PrepareScrub(ctx, c)
	if err != nil {
		return types.PayloadRehearsalMetrics{}, xerrors.Errorf("prepare payload rehearsal scrub: %w", err)
	}
	if result, err = liveResult(result); err != nil {
		_ = u.scrubber.ReleaseScrub(context.WithoutCancel(ctx), handle)
		_ = u.scrubber.CloseScrub(handle)
		return result, err
	}
	defer func() { _ = u.scrubber.CloseScrub(handle) }()
	completed := false
	defer func() {
		if !completed {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_ = u.scrubber.ReleaseScrub(releaseCtx, handle)
		}
	}()
	deadline := time.Now().Add(c.ScrubTimeLimit)
	for _, field := range types.OrderedPayloadRehearsalFields() {
		for time.Now().Before(deadline) && result.StoredBytes < c.ScrubByteLimit {
			var done bool
			result, done, err = u.scrubber.AdvanceScrubField(ctx, handle, field)
			if err != nil {
				return result, xerrors.Errorf("advance payload rehearsal scrub: %w", err)
			}
			if result, err = liveResult(result); err != nil {
				return result, err
			}
			if done {
				break
			}
		}
		if !time.Now().Before(deadline) || result.StoredBytes >= c.ScrubByteLimit {
			return result, errors.New("scrub resource cap reached; resume scrub with the persisted checkpoint")
		}
	}
	result, err = u.scrubber.CompleteScrub(ctx, handle)
	if err != nil {
		return result, xerrors.Errorf("complete payload rehearsal scrub: %w", err)
	}
	if result, err = liveResult(result); err != nil {
		return result, err
	}
	completed = true
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
	result = evaluated(result)
	if result.State != "rolled_back" || !result.RollbackVerified || result.ActivationReadiness.ActivationAllowed {
		return types.PayloadRehearsalMetrics{}, ErrUnsafePayloadRehearsalTransition
	}
	return result, nil
}
