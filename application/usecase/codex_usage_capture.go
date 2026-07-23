package usecase

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// CodexUsageCaptureInput is one body-free Codex terminal boundary.
type CodexUsageCaptureInput struct {
	SessionID          types.SessionID
	DeliveryID         string
	FallbackSourceName string
	FallbackTerminal   types.UsageTerminalCode
}

// CodexUsageCaptureResult exposes idempotent write outcomes for diagnostics.
type CodexUsageCaptureResult struct {
	Applied        int
	AlreadyApplied int
	Unavailable    int
}

// CodexUsageCaptureUsecase records mutually exclusive rollout or headless
// terminal observations in the provider-neutral usage ledger.
type CodexUsageCaptureUsecase interface {
	Capture(context.Context, CodexUsageCaptureInput) (CodexUsageCaptureResult, error)
	CaptureHeadless(context.Context, CodexUsageCaptureInput, application.CodexUsageLoadResult) (CodexUsageCaptureResult, error)
}

type codexUsageCaptureUsecase struct {
	source     application.CodexUsageSource
	repository model.UsageObservationRepository
}

// NewCodexUsageCaptureUsecase creates the Codex adapter boundary.
func NewCodexUsageCaptureUsecase(
	source application.CodexUsageSource,
	repository model.UsageObservationRepository,
) CodexUsageCaptureUsecase {
	return &codexUsageCaptureUsecase{source: source, repository: repository}
}

func (u *codexUsageCaptureUsecase) Capture(
	ctx context.Context,
	input CodexUsageCaptureInput,
) (CodexUsageCaptureResult, error) {
	if u.source == nil {
		return CodexUsageCaptureResult{}, xerrors.Errorf("Codex usage source must be configured")
	}
	loaded, err := u.source.Load(ctx, application.CodexUsageLoadCriteria{SessionID: input.SessionID})
	if err != nil {
		return CodexUsageCaptureResult{}, xerrors.Errorf("failed to load Codex usage source: %w", err)
	}
	return u.captureLoaded(ctx, input, loaded)
}

func (u *codexUsageCaptureUsecase) CaptureHeadless(
	ctx context.Context,
	input CodexUsageCaptureInput,
	loaded application.CodexUsageLoadResult,
) (CodexUsageCaptureResult, error) {
	return u.captureLoaded(ctx, input, loaded)
}

func (u *codexUsageCaptureUsecase) captureLoaded(
	ctx context.Context,
	input CodexUsageCaptureInput,
	loaded application.CodexUsageLoadResult,
) (CodexUsageCaptureResult, error) {
	if u.repository == nil {
		return CodexUsageCaptureResult{}, xerrors.Errorf("Codex usage repository must be configured")
	}
	if _, err := types.SessionIDFrom(input.SessionID.String()); err != nil {
		return CodexUsageCaptureResult{}, xerrors.Errorf("invalid Codex usage session: %w", err)
	}
	result := CodexUsageCaptureResult{}
	for _, sample := range loaded.Samples {
		accounting, err := u.sampleAccounting(ctx, sample)
		if err != nil {
			return result, err
		}
		observation, err := codexUsageObservation(input.SessionID, sample, accounting)
		if err != nil {
			return result, err
		}
		transition, err := u.repository.Record(ctx, observation)
		if err != nil {
			return result, xerrors.Errorf("failed to reconcile Codex usage sample %q: %w", sample.RecordID, err)
		}
		countUsageTransition(&result, transition)
		if !sample.Available && transition == model.UsageObservationTransitionApplied {
			result.Unavailable++
		}
	}
	if loaded.BoundaryObserved || strings.TrimSpace(input.DeliveryID) == "" {
		return result, nil
	}
	if err := u.recordUnavailableBoundary(ctx, input, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (u *codexUsageCaptureUsecase) sampleAccounting(
	ctx context.Context,
	sample application.CodexUsageSample,
) (types.UsageAccounting, error) {
	if !sample.Available {
		return types.UsageAccountingExcluded, nil
	}
	if strings.TrimSpace(sample.SuppressionID) == "" {
		return types.UsageAccountingAdditive, nil
	}
	suppressionID, err := types.UsageObservationIDFrom("codex:" + strings.TrimSpace(sample.SuppressionID))
	if err != nil {
		return "", xerrors.Errorf("invalid Codex headless suppression identity: %w", err)
	}
	existing, err := u.repository.FindByID(ctx, suppressionID)
	if err != nil {
		return "", xerrors.Errorf("failed to inspect Codex headless suppression identity: %w", err)
	}
	if _, present := existing.Value(); present {
		return types.UsageAccountingExcluded, nil
	}
	return types.UsageAccountingAdditive, nil
}

func (u *codexUsageCaptureUsecase) recordUnavailableBoundary(
	ctx context.Context,
	input CodexUsageCaptureInput,
	result *CodexUsageCaptureResult,
) error {
	sourceName := strings.TrimSpace(input.FallbackSourceName)
	if sourceName == "" {
		sourceName = "stop_hook"
	}
	terminal := input.FallbackTerminal
	if terminal == "" {
		terminal = types.UsageTerminalUnknown
	}
	id, err := types.UsageObservationIDFrom("codex:" + sourceName + ":" + input.SessionID.String() + ":" + strings.TrimSpace(input.DeliveryID))
	if err != nil {
		return xerrors.Errorf("invalid Codex unavailable observation identity: %w", err)
	}
	source, err := types.UsageSourceOf("codex", sourceName, "schema-v1", "openai", "")
	if err != nil {
		return xerrors.Errorf("invalid Codex unavailable source: %w", err)
	}
	// A body-free Stop delivery has no authoritative event timestamp. Use one
	// deterministic sentinel so exact replays are reconciled by the domain
	// aggregate instead of an adapter-local equality rule.
	now := time.Unix(0, 0).UTC()
	descriptor, err := model.NewUsageObservationDescriptor(
		id, input.SessionID, source, types.UsageScopeCall, types.UsageAccountingExcluded, now,
	)
	if err != nil {
		return xerrors.Errorf("invalid Codex unavailable descriptor: %w", err)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(unavailable, unavailable, unavailable, unavailable, unavailable, unavailable)
	if err != nil {
		return xerrors.Errorf("invalid Codex unavailable counters: %w", err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), terminal, now,
	)
	if err != nil {
		return xerrors.Errorf("invalid Codex unavailable observation: %w", err)
	}
	transition, err := u.repository.Record(ctx, observation)
	if err != nil {
		return xerrors.Errorf("failed to record Codex unavailable observation: %w", err)
	}
	countUsageTransition(result, transition)
	if transition == model.UsageObservationTransitionApplied {
		result.Unavailable++
	}
	return nil
}

func codexUsageObservation(
	sessionID types.SessionID,
	sample application.CodexUsageSample,
	accounting types.UsageAccounting,
) (*model.UsageObservation, error) {
	id, err := types.UsageObservationIDFrom("codex:" + strings.TrimSpace(sample.RecordID))
	if err != nil {
		return nil, xerrors.Errorf("invalid Codex usage identity: %w", err)
	}
	source, err := types.UsageSourceOf("codex", sample.SourceName, sample.SourceVersion, "openai", sample.Model)
	if err != nil {
		return nil, xerrors.Errorf("invalid Codex usage source: %w", err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		id, sessionID, source, types.UsageScopeCall, accounting, sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Codex usage descriptor: %w", err)
	}
	counters, err := codexUsageCounters(sample.Counters, sample.Available)
	if err != nil {
		return nil, err
	}
	terminal := sample.TerminalCode
	if terminal == "" {
		terminal = types.UsageTerminalUnknown
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), terminal, sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Codex usage observation: %w", err)
	}
	return observation, nil
}

func codexUsageCounters(raw application.CodexUsageCounters, available bool) (types.UsageCounters, error) {
	if !available {
		unavailable := types.UnavailableUsageValue()
		counters, err := types.UsageCountersOf(unavailable, unavailable, unavailable, unavailable, unavailable, unavailable)
		if err != nil {
			return types.UsageCounters{}, xerrors.Errorf("invalid unavailable Codex usage counters: %w", err)
		}
		return counters, nil
	}
	values := make([]types.UsageValue, 0, 6)
	for _, value := range []*int64{
		raw.InputTokens, raw.CachedInputTokens, raw.CacheWriteInputTokens,
		raw.OutputTokens, raw.ReasoningOutputTokens, raw.TotalTokens,
	} {
		if value == nil {
			values = append(values, types.UnavailableUsageValue())
			continue
		}
		known, err := types.KnownUsageValue(*value)
		if err != nil {
			return types.UsageCounters{}, xerrors.Errorf("invalid Codex usage counter: %w", err)
		}
		values = append(values, known)
	}
	counters, err := types.UsageCountersOf(values[0], values[1], values[2], values[3], values[4], values[5])
	if err != nil {
		return types.UsageCounters{}, xerrors.Errorf("invalid Codex usage counters: %w", err)
	}
	return counters, nil
}

func countUsageTransition(result *CodexUsageCaptureResult, transition model.UsageObservationTransition) {
	switch transition {
	case model.UsageObservationTransitionApplied:
		result.Applied++
	case model.UsageObservationTransitionAlreadyApplied:
		result.AlreadyApplied++
	}
}
