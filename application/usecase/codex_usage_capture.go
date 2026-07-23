package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// CodexUsageCaptureInput is the body-free Stop boundary consumed by the
// Codex adapter. DeliveryID must come from a verified host-native event_id.
type CodexUsageCaptureInput struct {
	SessionID  types.SessionID
	DeliveryID string
}

// CodexUsageCaptureResult exposes idempotent write outcomes for tests and
// future diagnostics without exposing transcript content.
type CodexUsageCaptureResult struct {
	Applied        int
	AlreadyApplied int
	Unavailable    int
}

// CodexUsageCaptureUsecase maps verified Codex source records into the
// provider-neutral usage aggregate.
type CodexUsageCaptureUsecase interface {
	Capture(context.Context, CodexUsageCaptureInput) (CodexUsageCaptureResult, error)
}

type codexUsageCaptureUsecase struct {
	source     application.CodexUsageSource
	repository model.UsageObservationRepository
	clock      types.Clock
}

// NewCodexUsageCaptureUsecase creates the Codex adapter boundary.
func NewCodexUsageCaptureUsecase(
	source application.CodexUsageSource,
	repository model.UsageObservationRepository,
	clock types.Clock,
) CodexUsageCaptureUsecase {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &codexUsageCaptureUsecase{source: source, repository: repository, clock: clock}
}

func (u *codexUsageCaptureUsecase) Capture(
	ctx context.Context,
	input CodexUsageCaptureInput,
) (CodexUsageCaptureResult, error) {
	if u.source == nil || u.repository == nil {
		return CodexUsageCaptureResult{}, xerrors.Errorf("Codex usage source and repository must be configured")
	}
	if _, err := types.SessionIDFrom(input.SessionID.String()); err != nil {
		return CodexUsageCaptureResult{}, xerrors.Errorf("invalid Codex usage session: %w", err)
	}
	loaded, err := u.source.Load(ctx, application.CodexUsageLoadCriteria{SessionID: input.SessionID})
	if err != nil {
		return CodexUsageCaptureResult{}, xerrors.Errorf("failed to load Codex usage source: %w", err)
	}
	result := CodexUsageCaptureResult{}
	for _, sample := range loaded.Samples {
		id, err := codexUsageObservationID(sample)
		if err != nil {
			return result, err
		}
		existing, err := u.repository.FindByID(ctx, id)
		if err != nil {
			return result, xerrors.Errorf("failed to inspect Codex usage sample %q: %w", sample.RecordID, err)
		}
		if current, present := existing.Value(); present {
			if !codexUsageSampleMatches(current, input.SessionID, sample) {
				return result, xerrors.Errorf("Codex usage sample %q changed semantics: %w", sample.RecordID, model.ErrConflictingUsageObservation)
			}
			result.AlreadyApplied++
			continue
		}
		observation, err := codexUsageObservation(input.SessionID, sample)
		if err != nil {
			return result, err
		}
		transition, err := u.repository.Record(ctx, observation)
		if err != nil {
			return result, xerrors.Errorf("failed to record Codex usage sample %q: %w", sample.RecordID, err)
		}
		countUsageTransition(&result, transition)
	}
	if len(loaded.Samples) > 0 || strings.TrimSpace(input.DeliveryID) == "" {
		return result, nil
	}
	if err := u.recordUnavailableBoundary(ctx, input, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (u *codexUsageCaptureUsecase) recordUnavailableBoundary(
	ctx context.Context,
	input CodexUsageCaptureInput,
	result *CodexUsageCaptureResult,
) error {
	id, err := types.UsageObservationIDFrom("codex:stop:" + input.SessionID.String() + ":" + strings.TrimSpace(input.DeliveryID))
	if err != nil {
		return xerrors.Errorf("invalid Codex unavailable observation identity: %w", err)
	}
	existing, err := u.repository.FindByID(ctx, id)
	if err != nil {
		return xerrors.Errorf("failed to inspect Codex unavailable observation: %w", err)
	}
	if _, present := existing.Value(); present {
		current, _ := existing.Value()
		if codexUnavailableBoundaryMatches(current, input.SessionID) {
			result.AlreadyApplied++
			return nil
		}
		return xerrors.Errorf("Codex unavailable boundary changed semantics: %w", model.ErrConflictingUsageObservation)
	}
	source, err := types.UsageSourceOf("codex", "stop_hook", "schema-v1", "openai", "")
	if err != nil {
		return xerrors.Errorf("invalid Codex unavailable source: %w", err)
	}
	now := u.clock.Now().UTC()
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
		descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalUnknown, now,
	)
	if err != nil {
		return xerrors.Errorf("invalid Codex unavailable observation: %w", err)
	}
	transition, err := u.repository.Record(ctx, observation)
	if err != nil {
		// A concurrent duplicate can win after FindByID. Re-read and accept
		// only an actual durable row; semantic conflicts remain fail-closed.
		after, findErr := u.repository.FindByID(ctx, id)
		if findErr == nil {
			if current, present := after.Value(); present && codexUnavailableBoundaryMatches(current, input.SessionID) {
				result.AlreadyApplied++
				return nil
			}
		}
		return xerrors.Errorf("failed to record Codex unavailable observation: %w", err)
	}
	countUsageTransition(result, transition)
	result.Unavailable++
	return nil
}

func codexUsageObservation(
	sessionID types.SessionID,
	sample application.CodexUsageSample,
) (*model.UsageObservation, error) {
	id, err := codexUsageObservationID(sample)
	if err != nil {
		return nil, err
	}
	source, err := types.UsageSourceOf("codex", sample.SourceName, sample.SourceVersion, "openai", sample.Model)
	if err != nil {
		return nil, xerrors.Errorf("invalid Codex usage source: %w", err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		id, sessionID, source, types.UsageScopeCall, types.UsageAccountingAdditive, sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Codex usage descriptor: %w", err)
	}
	counters, err := codexUsageCounters(sample.Counters)
	if err != nil {
		return nil, err
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalSuccess, sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Codex usage observation: %w", err)
	}
	return observation, nil
}

func codexUsageObservationID(sample application.CodexUsageSample) (types.UsageObservationID, error) {
	id, err := types.UsageObservationIDFrom("codex:" + strings.TrimSpace(sample.RecordID))
	if err != nil {
		return "", xerrors.Errorf("invalid Codex usage identity: %w", err)
	}
	return id, nil
}

func codexUsageSampleMatches(
	existing *model.UsageObservation,
	sessionID types.SessionID,
	sample application.CodexUsageSample,
) bool {
	if existing == nil || existing.Descriptor().SessionID() != sessionID || existing.Descriptor().Scope() != types.UsageScopeCall || existing.Descriptor().Accounting() != types.UsageAccountingAdditive || existing.Status() != types.UsageObservationFinalized || !existing.Descriptor().ObservedAt().Equal(sample.ObservedAt.UTC()) {
		return false
	}
	source := existing.Descriptor().Source()
	if source.Host() != "codex" || source.Name() != strings.TrimSpace(sample.SourceName) || source.Version() != strings.TrimSpace(sample.SourceVersion) || source.Provider() != "openai" || source.Model() != strings.TrimSpace(sample.Model) {
		return false
	}
	want, err := codexUsageCounters(sample.Counters)
	if err != nil || !usageCountersEqual(existing.Counters(), want) || existing.Cost().State() != types.UsageCostUnavailable {
		return false
	}
	terminal, present := existing.TerminalCode().Value()
	return present && terminal == types.UsageTerminalSuccess
}

func codexUnavailableBoundaryMatches(existing *model.UsageObservation, sessionID types.SessionID) bool {
	if existing == nil || existing.Descriptor().SessionID() != sessionID || existing.Descriptor().Scope() != types.UsageScopeCall || existing.Descriptor().Accounting() != types.UsageAccountingExcluded || existing.Status() != types.UsageObservationFinalized {
		return false
	}
	source := existing.Descriptor().Source()
	if source.Host() != "codex" || source.Name() != "stop_hook" || source.Version() != "schema-v1" || source.Provider() != "openai" || source.Model() != "" || existing.Counters().Availability() != types.UsageAvailabilityUnavailable || existing.Cost().State() != types.UsageCostUnavailable {
		return false
	}
	terminal, present := existing.TerminalCode().Value()
	return present && terminal == types.UsageTerminalUnknown
}

func usageCountersEqual(left, right types.UsageCounters) bool {
	leftValues := []types.UsageValue{left.Input(), left.CachedInput(), left.CacheWriteInput(), left.Output(), left.ReasoningOutput(), left.Total()}
	rightValues := []types.UsageValue{right.Input(), right.CachedInput(), right.CacheWriteInput(), right.Output(), right.ReasoningOutput(), right.Total()}
	for i := range leftValues {
		if leftValues[i].State() != rightValues[i].State() {
			return false
		}
		leftValue, leftPresent := leftValues[i].Value()
		rightValue, rightPresent := rightValues[i].Value()
		if leftPresent != rightPresent || leftValue != rightValue {
			return false
		}
	}
	return true
}

func codexUsageCounters(raw application.CodexUsageCounters) (types.UsageCounters, error) {
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
