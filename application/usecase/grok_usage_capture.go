package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// GrokUsageCaptureInput identifies one body-free Grok boundary.
type GrokUsageCaptureInput struct {
	SessionID        types.SessionID
	DeliveryID       string
	FallbackTerminal types.UsageTerminalCode
}

// GrokUsageCaptureResult exposes idempotent write outcomes.
type GrokUsageCaptureResult struct {
	Applied        int
	AlreadyApplied int
	Unavailable    int
}

// GrokUsageCaptureUsecase records terminal headless results and explicit
// unavailable native Stop boundaries.
type GrokUsageCaptureUsecase interface {
	CaptureHeadless(context.Context, GrokUsageCaptureInput, application.GrokUsageLoadResult) (GrokUsageCaptureResult, error)
	CaptureHookUnavailable(context.Context, GrokUsageCaptureInput) (GrokUsageCaptureResult, error)
}

type grokUsageCaptureUsecase struct {
	repository application.GrokUsageRepository
}

// NewGrokUsageCaptureUsecase creates the Grok adapter boundary.
func NewGrokUsageCaptureUsecase(repository application.GrokUsageRepository) GrokUsageCaptureUsecase {
	return &grokUsageCaptureUsecase{repository: repository}
}

func (u *grokUsageCaptureUsecase) CaptureHeadless(
	ctx context.Context,
	input GrokUsageCaptureInput,
	loaded application.GrokUsageLoadResult,
) (GrokUsageCaptureResult, error) {
	if err := u.validateInput(input); err != nil {
		return GrokUsageCaptureResult{}, err
	}
	if len(loaded.Samples) > 1 ||
		(len(loaded.Samples) == 1 && !loaded.BoundaryObserved) ||
		(loaded.BoundaryObserved && len(loaded.Samples) == 0 &&
			!validGrokUsageIdentity(strings.TrimSpace(loaded.TerminalRecordID))) {
		return GrokUsageCaptureResult{}, xerrors.Errorf("inconsistent Grok terminal boundary and samples")
	}
	result := GrokUsageCaptureResult{}
	if len(loaded.Samples) == 1 {
		observation, err := grokUsageObservation(input.SessionID, loaded.Samples[0])
		if err != nil {
			return result, xerrors.Errorf("failed to map Grok usage sample: %w", err)
		}
		transition, err := u.recordPortableHeadless(ctx, observation)
		if err != nil {
			return result, xerrors.Errorf("failed to reconcile Grok usage sample: %w", err)
		}
		countGrokUsageTransition(&result, transition)
		return result, nil
	}
	if loaded.BoundaryObserved {
		return u.recordUnavailable(
			ctx,
			input,
			types.UsageScopeRun,
			"headless_stream",
			"0.2.106",
			loaded.TerminalRecordID,
			loaded.TerminalCode,
			result,
		)
	}
	return u.recordUnavailable(
		ctx,
		input,
		types.UsageScopeRun,
		"headless_stream",
		"schema-v1",
		"",
		input.FallbackTerminal,
		result,
	)
}

func (u *grokUsageCaptureUsecase) recordPortableHeadless(
	ctx context.Context,
	proposed *model.UsageObservation,
) (model.UsageObservationTransition, error) {
	id := proposed.Descriptor().ObservationID()
	existing, err := u.repository.FindByID(ctx, id)
	if err != nil {
		return "", xerrors.Errorf("failed to inspect Grok provider identity: %w", err)
	}
	if current, present := existing.Value(); present {
		return reconcilePortableGrokObservation(current, proposed)
	}
	transition, err := u.repository.Record(ctx, proposed)
	if err == nil {
		return transition, nil
	}
	if !errors.Is(err, model.ErrConflictingUsageObservation) {
		return "", xerrors.Errorf("failed to record Grok provider identity: %w", err)
	}
	// Another process may have committed the same provider terminal after the
	// lookup. Re-read it so cross-session redelivery remains idempotent while a
	// payload change for that provider identity still fails closed.
	existing, lookupErr := u.repository.FindByID(ctx, id)
	if lookupErr != nil {
		return "", errors.Join(err, lookupErr)
	}
	if current, present := existing.Value(); present {
		return reconcilePortableGrokObservation(current, proposed)
	}
	return "", xerrors.Errorf("Grok provider identity conflict disappeared: %w", err)
}

func reconcilePortableGrokObservation(
	current, proposed *model.UsageObservation,
) (model.UsageObservationTransition, error) {
	currentDescriptor := current.Descriptor()
	proposedDescriptor := proposed.Descriptor()
	currentTerminal, currentTerminalPresent := current.TerminalCode().Value()
	proposedTerminal, proposedTerminalPresent := proposed.TerminalCode().Value()
	equivalent := currentDescriptor.ObservationID() == proposedDescriptor.ObservationID() &&
		currentDescriptor.Source() == proposedDescriptor.Source() &&
		currentDescriptor.Scope() == proposedDescriptor.Scope() &&
		currentDescriptor.Accounting() == proposedDescriptor.Accounting() &&
		current.Status() == proposed.Status() &&
		current.Counters() == proposed.Counters() &&
		current.Cost() == proposed.Cost() &&
		currentTerminalPresent == proposedTerminalPresent &&
		(!currentTerminalPresent || currentTerminal == proposedTerminal)
	if equivalent {
		return model.UsageObservationTransitionAlreadyApplied, nil
	}
	return "", xerrors.Errorf(
		"provider terminal %s changed across Traceary sessions: %w",
		currentDescriptor.ObservationID(),
		model.ErrConflictingUsageObservation,
	)
}

func (u *grokUsageCaptureUsecase) CaptureHookUnavailable(
	ctx context.Context,
	input GrokUsageCaptureInput,
) (GrokUsageCaptureResult, error) {
	if err := u.validateInput(input); err != nil {
		return GrokUsageCaptureResult{}, err
	}
	if strings.TrimSpace(input.DeliveryID) == "" {
		return GrokUsageCaptureResult{}, nil
	}
	return u.recordUnavailable(
		ctx,
		input,
		types.UsageScopeCall,
		"stop_hook",
		"schema-v1",
		"",
		input.FallbackTerminal,
		GrokUsageCaptureResult{},
	)
}

func (u *grokUsageCaptureUsecase) validateInput(input GrokUsageCaptureInput) error {
	if u.repository == nil {
		return xerrors.Errorf("Grok usage repository must be configured")
	}
	if _, err := types.SessionIDFrom(input.SessionID.String()); err != nil {
		return xerrors.Errorf("invalid Grok usage session: %w", err)
	}
	return nil
}

func (u *grokUsageCaptureUsecase) recordUnavailable(
	ctx context.Context,
	input GrokUsageCaptureInput,
	scope types.UsageScope,
	sourceName string,
	sourceVersion string,
	providerRecordID string,
	terminal types.UsageTerminalCode,
	result GrokUsageCaptureResult,
) (GrokUsageCaptureResult, error) {
	identity := input.SessionID.String() + "\x00" + strings.TrimSpace(input.DeliveryID)
	if strings.TrimSpace(providerRecordID) != "" {
		identity = strings.TrimSpace(providerRecordID)
	}
	digest := sha256.Sum256([]byte(identity))
	id, err := types.UsageObservationIDFrom("grok:" + sourceName + ":" + hex.EncodeToString(digest[:]))
	if err != nil {
		return result, xerrors.Errorf("invalid Grok unavailable observation identity: %w", err)
	}
	source, err := types.UsageSourceOf("grok", sourceName, sourceVersion, "xai", "")
	if err != nil {
		return result, xerrors.Errorf("invalid Grok unavailable source: %w", err)
	}
	observedAt := time.Unix(0, 0).UTC()
	descriptor, err := model.NewUsageObservationDescriptor(
		id, input.SessionID, source, scope, types.UsageAccountingExcluded, observedAt,
	)
	if err != nil {
		return result, xerrors.Errorf("invalid Grok unavailable descriptor: %w", err)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(
		unavailable, unavailable, unavailable, unavailable, unavailable, unavailable,
	)
	if err != nil {
		return result, xerrors.Errorf("invalid Grok unavailable counters: %w", err)
	}
	if terminal == "" {
		terminal = types.UsageTerminalUnknown
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), terminal, observedAt,
	)
	if err != nil {
		return result, xerrors.Errorf("invalid Grok unavailable observation: %w", err)
	}
	var transition model.UsageObservationTransition
	if strings.TrimSpace(providerRecordID) != "" {
		transition, err = u.recordPortableHeadless(ctx, observation)
	} else {
		transition, err = u.repository.Record(ctx, observation)
	}
	if err != nil {
		return result, xerrors.Errorf("failed to record Grok unavailable observation: %w", err)
	}
	countGrokUsageTransition(&result, transition)
	if transition == model.UsageObservationTransitionApplied {
		result.Unavailable++
	}
	return result, nil
}

func grokUsageObservation(
	sessionID types.SessionID,
	sample application.GrokUsageSample,
) (*model.UsageObservation, error) {
	if !sample.Available {
		return nil, xerrors.Errorf("Grok terminal sample must be available")
	}
	recordID := strings.TrimSpace(sample.RecordID)
	if !validGrokUsageIdentity(recordID) {
		return nil, xerrors.Errorf("invalid Grok usage record identity")
	}
	digest := sha256.Sum256([]byte(recordID))
	id, err := types.UsageObservationIDFrom("grok:headless_stream:" + hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, xerrors.Errorf("invalid Grok usage identity: %w", err)
	}
	source, err := types.UsageSourceOf(
		"grok", sample.SourceName, sample.SourceVersion, "xai", sample.Model,
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Grok usage source: %w", err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		id, sessionID, source, types.UsageScopeRun, types.UsageAccountingAdditive, sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Grok usage descriptor: %w", err)
	}
	counters, err := grokUsageCounters(sample.Counters)
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
		return nil, xerrors.Errorf("invalid Grok usage observation: %w", err)
	}
	return observation, nil
}

func grokUsageCounters(raw application.GrokUsageCounters) (types.UsageCounters, error) {
	values := make([]types.UsageValue, 0, 5)
	for _, value := range []*int64{
		raw.InputTokens, raw.CachedInputTokens, raw.OutputTokens, raw.ReasoningTokens, raw.TotalTokens,
	} {
		if value == nil {
			values = append(values, types.UnavailableUsageValue())
			continue
		}
		known, err := types.KnownUsageValue(*value)
		if err != nil {
			return types.UsageCounters{}, xerrors.Errorf("invalid Grok usage counter: %w", err)
		}
		values = append(values, known)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(
		values[0], values[1], unavailable, values[2], values[3], values[4],
	)
	if err != nil {
		return types.UsageCounters{}, xerrors.Errorf("invalid Grok usage counters: %w", err)
	}
	return counters, nil
}

func validGrokUsageIdentity(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00")
}

func countGrokUsageTransition(result *GrokUsageCaptureResult, transition model.UsageObservationTransition) {
	switch transition {
	case model.UsageObservationTransitionApplied:
		result.Applied++
	case model.UsageObservationTransitionAlreadyApplied:
		result.AlreadyApplied++
	}
}
