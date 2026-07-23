package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// GeminiUsageCaptureInput identifies one body-free Gemini boundary.
type GeminiUsageCaptureInput struct {
	SessionID        types.SessionID
	DeliveryID       string
	FallbackTerminal types.UsageTerminalCode
}

// GeminiUsageCaptureResult exposes idempotent write outcomes.
type GeminiUsageCaptureResult struct {
	Applied        int
	AlreadyApplied int
	Unavailable    int
}

// GeminiUsageCaptureUsecase records terminal one-shot results and explicit
// unavailable interactive boundaries.
type GeminiUsageCaptureUsecase interface {
	CaptureHeadless(context.Context, GeminiUsageCaptureInput, application.GeminiUsageLoadResult) (GeminiUsageCaptureResult, error)
	CaptureInteractiveUnavailable(context.Context, GeminiUsageCaptureInput) (GeminiUsageCaptureResult, error)
}

type geminiUsageCaptureUsecase struct {
	repository application.GeminiUsageRepository
}

// NewGeminiUsageCaptureUsecase creates the Gemini adapter boundary.
func NewGeminiUsageCaptureUsecase(
	repository application.GeminiUsageRepository,
) GeminiUsageCaptureUsecase {
	return &geminiUsageCaptureUsecase{repository: repository}
}

func (u *geminiUsageCaptureUsecase) CaptureHeadless(
	ctx context.Context,
	input GeminiUsageCaptureInput,
	loaded application.GeminiUsageLoadResult,
) (GeminiUsageCaptureResult, error) {
	if err := u.validateInput(input); err != nil {
		return GeminiUsageCaptureResult{}, err
	}
	if loaded.BoundaryObserved != (len(loaded.Samples) > 0) {
		return GeminiUsageCaptureResult{}, xerrors.Errorf("inconsistent Gemini terminal boundary and samples")
	}
	result := GeminiUsageCaptureResult{}
	for _, sample := range loaded.Samples {
		observation, err := geminiUsageObservation(input.SessionID, sample)
		if err != nil {
			return result, xerrors.Errorf("failed to map Gemini usage sample: %w", err)
		}
		transition, err := u.repository.Record(ctx, observation)
		if err != nil {
			return result, xerrors.Errorf("failed to reconcile Gemini usage sample: %w", err)
		}
		countGeminiUsageTransition(&result, transition)
	}
	if loaded.BoundaryObserved {
		return result, nil
	}
	if err := u.recordUnavailable(ctx, input, types.UsageScopeRun, "headless_stream", &result); err != nil {
		return result, err
	}
	return result, nil
}

func (u *geminiUsageCaptureUsecase) CaptureInteractiveUnavailable(
	ctx context.Context,
	input GeminiUsageCaptureInput,
) (GeminiUsageCaptureResult, error) {
	if err := u.validateInput(input); err != nil {
		return GeminiUsageCaptureResult{}, err
	}
	result := GeminiUsageCaptureResult{}
	if strings.TrimSpace(input.DeliveryID) == "" {
		return result, nil
	}
	if err := u.recordUnavailable(ctx, input, types.UsageScopeCall, "after_agent_hook", &result); err != nil {
		return result, err
	}
	return result, nil
}

func (u *geminiUsageCaptureUsecase) validateInput(input GeminiUsageCaptureInput) error {
	if u.repository == nil {
		return xerrors.Errorf("Gemini usage repository must be configured")
	}
	if _, err := types.SessionIDFrom(input.SessionID.String()); err != nil {
		return xerrors.Errorf("invalid Gemini usage session: %w", err)
	}
	return nil
}

func (u *geminiUsageCaptureUsecase) recordUnavailable(
	ctx context.Context,
	input GeminiUsageCaptureInput,
	scope types.UsageScope,
	sourceName string,
	result *GeminiUsageCaptureResult,
) error {
	identity := input.SessionID.String() + "\x00" + strings.TrimSpace(input.DeliveryID)
	digest := sha256.Sum256([]byte(identity))
	id, err := types.UsageObservationIDFrom("gemini:" + sourceName + ":" + hex.EncodeToString(digest[:]))
	if err != nil {
		return xerrors.Errorf("invalid Gemini unavailable observation identity: %w", err)
	}
	source, err := types.UsageSourceOf("gemini", sourceName, "schema-v1", "google", "")
	if err != nil {
		return xerrors.Errorf("invalid Gemini unavailable source: %w", err)
	}
	observedAt := time.Unix(0, 0).UTC()
	descriptor, err := model.NewUsageObservationDescriptor(
		id, input.SessionID, source, scope, types.UsageAccountingExcluded, observedAt,
	)
	if err != nil {
		return xerrors.Errorf("invalid Gemini unavailable descriptor: %w", err)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(
		unavailable, unavailable, unavailable, unavailable, unavailable, unavailable,
	)
	if err != nil {
		return xerrors.Errorf("invalid Gemini unavailable counters: %w", err)
	}
	terminal := input.FallbackTerminal
	if terminal == "" {
		terminal = types.UsageTerminalUnknown
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), terminal, observedAt,
	)
	if err != nil {
		return xerrors.Errorf("invalid Gemini unavailable observation: %w", err)
	}
	transition, err := u.repository.Record(ctx, observation)
	if err != nil {
		return xerrors.Errorf("failed to record Gemini unavailable observation: %w", err)
	}
	countGeminiUsageTransition(result, transition)
	if transition == model.UsageObservationTransitionApplied {
		result.Unavailable++
	}
	return nil
}

func geminiUsageObservation(
	sessionID types.SessionID,
	sample application.GeminiUsageSample,
) (*model.UsageObservation, error) {
	if !sample.Available {
		return nil, xerrors.Errorf("Gemini terminal sample must be available")
	}
	recordID := strings.TrimSpace(sample.RecordID)
	if recordID == "" || len(recordID) > 512 || strings.ContainsAny(recordID, "\r\n\x00") {
		return nil, xerrors.Errorf("invalid Gemini usage record identity")
	}
	digest := sha256.Sum256([]byte(sessionID.String() + "\x00" + recordID))
	id, err := types.UsageObservationIDFrom("gemini:" + hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, xerrors.Errorf("invalid Gemini usage identity: %w", err)
	}
	source, err := types.UsageSourceOf(
		"gemini", sample.SourceName, sample.SourceVersion, "google", sample.Model,
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Gemini usage source: %w", err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		id, sessionID, source, types.UsageScopeRun, types.UsageAccountingAdditive, sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Gemini usage descriptor: %w", err)
	}
	counters, err := geminiUsageCounters(sample.Counters)
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
		return nil, xerrors.Errorf("invalid Gemini usage observation: %w", err)
	}
	return observation, nil
}

func geminiUsageCounters(raw application.GeminiUsageCounters) (types.UsageCounters, error) {
	values := make([]types.UsageValue, 0, 4)
	for _, value := range []*int64{
		raw.InputTokens, raw.CachedInputTokens, raw.OutputTokens, raw.TotalTokens,
	} {
		if value == nil {
			values = append(values, types.UnavailableUsageValue())
			continue
		}
		known, err := types.KnownUsageValue(*value)
		if err != nil {
			return types.UsageCounters{}, xerrors.Errorf("invalid Gemini usage counter: %w", err)
		}
		values = append(values, known)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(
		values[0], values[1], unavailable, values[2], unavailable, values[3],
	)
	if err != nil {
		return types.UsageCounters{}, xerrors.Errorf("invalid Gemini usage counters: %w", err)
	}
	return counters, nil
}

func countGeminiUsageTransition(result *GeminiUsageCaptureResult, transition model.UsageObservationTransition) {
	switch transition {
	case model.UsageObservationTransitionApplied:
		result.Applied++
	case model.UsageObservationTransitionAlreadyApplied:
		result.AlreadyApplied++
	}
}
