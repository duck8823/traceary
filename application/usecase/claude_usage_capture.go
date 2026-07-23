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

// ClaudeUsageCaptureInput is one body-free Claude terminal boundary.
type ClaudeUsageCaptureInput struct {
	SessionID          types.SessionID
	DeliveryID         string
	FallbackSourceName string
	FallbackTerminal   types.UsageTerminalCode
}

// ClaudeUsageCaptureResult exposes idempotent write outcomes for diagnostics.
type ClaudeUsageCaptureResult struct {
	Applied        int
	AlreadyApplied int
	Unavailable    int
}

// ClaudeUsageCaptureUsecase records transcript calls or a mutually exclusive
// one-shot terminal summary in the provider-neutral usage ledger.
type ClaudeUsageCaptureUsecase interface {
	Capture(context.Context, ClaudeUsageCaptureInput) (ClaudeUsageCaptureResult, error)
	CaptureHeadless(context.Context, ClaudeUsageCaptureInput, application.ClaudeUsageLoadResult) (ClaudeUsageCaptureResult, error)
}

type claudeUsageCaptureUsecase struct {
	source     application.ClaudeUsageSource
	repository application.ClaudeUsageRepository
}

// NewClaudeUsageCaptureUsecase creates the Claude adapter boundary.
func NewClaudeUsageCaptureUsecase(
	source application.ClaudeUsageSource,
	repository application.ClaudeUsageRepository,
) ClaudeUsageCaptureUsecase {
	return &claudeUsageCaptureUsecase{source: source, repository: repository}
}

func (u *claudeUsageCaptureUsecase) Capture(
	ctx context.Context,
	input ClaudeUsageCaptureInput,
) (ClaudeUsageCaptureResult, error) {
	if u.source == nil {
		return ClaudeUsageCaptureResult{}, xerrors.Errorf("Claude usage source must be configured")
	}
	loaded, err := u.source.Load(ctx, application.ClaudeUsageLoadCriteria{SessionID: input.SessionID})
	if err != nil {
		return ClaudeUsageCaptureResult{}, xerrors.Errorf("failed to load Claude usage source: %w", err)
	}
	return u.captureLoaded(ctx, input, loaded)
}

func (u *claudeUsageCaptureUsecase) CaptureHeadless(
	ctx context.Context,
	input ClaudeUsageCaptureInput,
	loaded application.ClaudeUsageLoadResult,
) (ClaudeUsageCaptureResult, error) {
	return u.captureLoaded(ctx, input, loaded)
}

func (u *claudeUsageCaptureUsecase) captureLoaded(
	ctx context.Context,
	input ClaudeUsageCaptureInput,
	loaded application.ClaudeUsageLoadResult,
) (ClaudeUsageCaptureResult, error) {
	if u.repository == nil {
		return ClaudeUsageCaptureResult{}, xerrors.Errorf("Claude usage repository must be configured")
	}
	if _, err := types.SessionIDFrom(input.SessionID.String()); err != nil {
		return ClaudeUsageCaptureResult{}, xerrors.Errorf("invalid Claude usage session: %w", err)
	}
	if loaded.Mode != application.ClaudeUsageModeTranscriptCalls &&
		loaded.Mode != application.ClaudeUsageModeOneShotStream {
		return ClaudeUsageCaptureResult{}, xerrors.Errorf("invalid Claude usage capture mode")
	}

	result := ClaudeUsageCaptureResult{}
	for _, sample := range loaded.Samples {
		accounting, err := claudeUsageAccounting(loaded.Mode, sample)
		if err != nil {
			return result, err
		}
		observation, err := claudeUsageObservation(input.SessionID, sample, accounting)
		if err != nil {
			return result, xerrors.Errorf("failed to map Claude usage sample %q: %w", sample.RecordID, err)
		}
		transition, err := u.repository.Record(ctx, observation)
		if err != nil {
			return result, xerrors.Errorf("failed to reconcile Claude usage sample %q: %w", sample.RecordID, err)
		}
		countClaudeUsageTransition(&result, transition)
		if !sample.Available && transition == model.UsageObservationTransitionApplied {
			result.Unavailable++
		}
	}
	if loaded.BoundaryObserved || strings.TrimSpace(input.DeliveryID) == "" {
		return result, nil
	}
	if err := u.recordUnavailableBoundary(ctx, input, loaded.Mode, &result); err != nil {
		return result, err
	}
	return result, nil
}

func claudeUsageAccounting(
	mode application.ClaudeUsageCaptureMode,
	sample application.ClaudeUsageSample,
) (types.UsageAccounting, error) {
	if !sample.Available {
		return types.UsageAccountingExcluded, nil
	}
	switch mode {
	case application.ClaudeUsageModeTranscriptCalls:
		if sample.Scope != types.UsageScopeCall {
			return "", xerrors.Errorf("Claude transcript mode requires call-scoped samples")
		}
		return types.UsageAccountingAdditive, nil
	case application.ClaudeUsageModeOneShotStream:
		switch sample.Scope {
		case types.UsageScopeRun:
			return types.UsageAccountingAdditive, nil
		case types.UsageScopeCall:
			// Complete legacy JSONL can contain both assistant usage and the
			// terminal summary. Retain calls as evidence without double counting.
			return types.UsageAccountingExcluded, nil
		default:
			return "", xerrors.Errorf("Claude one-shot mode has unsupported usage scope")
		}
	default:
		return "", xerrors.Errorf("invalid Claude usage capture mode")
	}
}

func (u *claudeUsageCaptureUsecase) recordUnavailableBoundary(
	ctx context.Context,
	input ClaudeUsageCaptureInput,
	mode application.ClaudeUsageCaptureMode,
	result *ClaudeUsageCaptureResult,
) error {
	sourceName := strings.TrimSpace(input.FallbackSourceName)
	if sourceName == "" {
		sourceName = "stop_hook"
	}
	terminal := input.FallbackTerminal
	if terminal == "" {
		terminal = types.UsageTerminalUnknown
	}
	id, err := types.UsageObservationIDFrom(
		"claude:" + sourceName + ":" + input.SessionID.String() + ":" + strings.TrimSpace(input.DeliveryID),
	)
	if err != nil {
		return xerrors.Errorf("invalid Claude unavailable observation identity: %w", err)
	}
	source, err := types.UsageSourceOf("claude", sourceName, "schema-v1", "anthropic", "")
	if err != nil {
		return xerrors.Errorf("invalid Claude unavailable source: %w", err)
	}
	scope := types.UsageScopeCall
	if mode == application.ClaudeUsageModeOneShotStream {
		scope = types.UsageScopeRun
	}
	now := time.Unix(0, 0).UTC()
	descriptor, err := model.NewUsageObservationDescriptor(
		id, input.SessionID, source, scope, types.UsageAccountingExcluded, now,
	)
	if err != nil {
		return xerrors.Errorf("invalid Claude unavailable descriptor: %w", err)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(unavailable, unavailable, unavailable, unavailable, unavailable, unavailable)
	if err != nil {
		return xerrors.Errorf("invalid Claude unavailable counters: %w", err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), terminal, now,
	)
	if err != nil {
		return xerrors.Errorf("invalid Claude unavailable observation: %w", err)
	}
	transition, err := u.repository.Record(ctx, observation)
	if err != nil {
		return xerrors.Errorf("failed to record Claude unavailable observation: %w", err)
	}
	countClaudeUsageTransition(result, transition)
	if transition == model.UsageObservationTransitionApplied {
		result.Unavailable++
	}
	return nil
}

func claudeUsageObservation(
	sessionID types.SessionID,
	sample application.ClaudeUsageSample,
	accounting types.UsageAccounting,
) (*model.UsageObservation, error) {
	id, err := types.UsageObservationIDFrom("claude:" + strings.TrimSpace(sample.RecordID))
	if err != nil {
		return nil, xerrors.Errorf("invalid Claude usage identity: %w", err)
	}
	source, err := types.UsageSourceOf(
		"claude", sample.SourceName, sample.SourceVersion, "anthropic", sample.Model,
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Claude usage source: %w", err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		id, sessionID, source, sample.Scope, accounting, sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Claude usage descriptor: %w", err)
	}
	counters, err := claudeUsageCounters(sample.Counters, sample.Available)
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
		return nil, xerrors.Errorf("invalid Claude usage observation: %w", err)
	}
	return observation, nil
}

func claudeUsageCounters(raw application.ClaudeUsageCounters, available bool) (types.UsageCounters, error) {
	if !available {
		unavailable := types.UnavailableUsageValue()
		counters, err := types.UsageCountersOf(unavailable, unavailable, unavailable, unavailable, unavailable, unavailable)
		if err != nil {
			return types.UsageCounters{}, xerrors.Errorf("invalid unavailable Claude usage counters: %w", err)
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
			return types.UsageCounters{}, xerrors.Errorf("invalid Claude usage counter: %w", err)
		}
		values = append(values, known)
	}
	counters, err := types.UsageCountersOf(values[0], values[1], values[2], values[3], values[4], values[5])
	if err != nil {
		return types.UsageCounters{}, xerrors.Errorf("invalid Claude usage counters: %w", err)
	}
	return counters, nil
}

func countClaudeUsageTransition(result *ClaudeUsageCaptureResult, transition model.UsageObservationTransition) {
	switch transition {
	case model.UsageObservationTransitionApplied:
		result.Applied++
	case model.UsageObservationTransitionAlreadyApplied:
		result.AlreadyApplied++
	}
}
