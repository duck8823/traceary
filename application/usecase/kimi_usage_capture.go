package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// KimiUsageBoundary identifies a lifecycle completion that has no proven
// one-to-one relationship with a Kimi wire usage record.
type KimiUsageBoundary string

const (
	// KimiUsageBoundaryStop identifies a Kimi Stop hook.
	KimiUsageBoundaryStop KimiUsageBoundary = "stop"
	// KimiUsageBoundarySessionEnd identifies a Kimi SessionEnd hook.
	KimiUsageBoundarySessionEnd KimiUsageBoundary = "session_end"
)

// KimiUsageCaptureInput identifies one body-free Kimi lifecycle boundary.
type KimiUsageCaptureInput struct {
	SessionID         types.SessionID
	ProviderSessionID string
	Boundary          KimiUsageBoundary
}

// KimiUsageCaptureResult exposes idempotent source and availability writes.
type KimiUsageCaptureResult struct {
	Applied        int
	AlreadyApplied int
	Unavailable    int
}

// KimiUsageCaptureUsecase records partial wire evidence and explicit
// unavailable lifecycle completions.
type KimiUsageCaptureUsecase interface {
	Capture(context.Context, KimiUsageCaptureInput) (KimiUsageCaptureResult, error)
}

type kimiUsageCaptureUsecase struct {
	source     application.KimiUsageSource
	repository application.KimiUsageRepository
}

// NewKimiUsageCaptureUsecase creates the Kimi usage adapter boundary.
func NewKimiUsageCaptureUsecase(
	source application.KimiUsageSource,
	repository application.KimiUsageRepository,
) KimiUsageCaptureUsecase {
	return &kimiUsageCaptureUsecase{source: source, repository: repository}
}

func (u *kimiUsageCaptureUsecase) Capture(
	ctx context.Context,
	input KimiUsageCaptureInput,
) (KimiUsageCaptureResult, error) {
	if err := u.validateInput(input); err != nil {
		return KimiUsageCaptureResult{}, err
	}
	loaded, err := u.source.Load(ctx, strings.TrimSpace(input.ProviderSessionID))
	if err != nil {
		return KimiUsageCaptureResult{}, xerrors.Errorf("failed to load Kimi usage source: %w", err)
	}
	if loaded.LatestTurnOrdinal < 0 {
		return KimiUsageCaptureResult{}, xerrors.Errorf("invalid Kimi usage turn ordinal")
	}

	result := KimiUsageCaptureResult{}
	for _, sample := range loaded.Samples {
		observation, err := kimiUsageObservation(input.SessionID, sample)
		if err != nil {
			return result, xerrors.Errorf("failed to map Kimi usage sample: %w", err)
		}
		transition, err := u.repository.Record(ctx, observation)
		if err != nil {
			return result, xerrors.Errorf("failed to record Kimi usage sample: %w", err)
		}
		countKimiUsageTransition(&result, transition)
	}
	if err := u.recordUnavailable(ctx, input, loaded.LatestTurnOrdinal, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (u *kimiUsageCaptureUsecase) validateInput(input KimiUsageCaptureInput) error {
	if u == nil || u.source == nil || u.repository == nil {
		return xerrors.Errorf("Kimi usage dependencies must be configured")
	}
	if _, err := types.SessionIDFrom(input.SessionID.String()); err != nil {
		return xerrors.Errorf("invalid Kimi usage session: %w", err)
	}
	if !validKimiUsageIdentity(input.ProviderSessionID) {
		return xerrors.Errorf("invalid Kimi provider session identity")
	}
	switch input.Boundary {
	case KimiUsageBoundaryStop, KimiUsageBoundarySessionEnd:
	default:
		return xerrors.Errorf("invalid Kimi usage boundary")
	}
	return nil
}

func (u *kimiUsageCaptureUsecase) recordUnavailable(
	ctx context.Context,
	input KimiUsageCaptureInput,
	turnOrdinal int64,
	result *KimiUsageCaptureResult,
) error {
	identity := strings.TrimSpace(input.ProviderSessionID) + "\x00" +
		string(input.Boundary) + "\x00" + strconv.FormatInt(turnOrdinal, 10)
	digest := sha256.Sum256([]byte(identity))
	id, err := types.UsageObservationIDFrom("kimi:" + string(input.Boundary) + "_hook:" + hex.EncodeToString(digest[:]))
	if err != nil {
		return xerrors.Errorf("invalid Kimi unavailable observation identity: %w", err)
	}
	source, err := types.UsageSourceOf("kimi", string(input.Boundary)+"_hook", "schema-v1", "moonshot", "")
	if err != nil {
		return xerrors.Errorf("invalid Kimi unavailable source: %w", err)
	}
	observedAt := time.Unix(0, 0).UTC()
	descriptor, err := model.NewUsageObservationDescriptor(
		id,
		input.SessionID,
		source,
		types.UsageScopeCall,
		types.UsageAccountingExcluded,
		observedAt,
	)
	if err != nil {
		return xerrors.Errorf("invalid Kimi unavailable descriptor: %w", err)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(
		unavailable, unavailable, unavailable, unavailable, unavailable, unavailable,
	)
	if err != nil {
		return xerrors.Errorf("invalid Kimi unavailable counters: %w", err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor,
		counters,
		types.UnavailableUsageCost(),
		types.UsageTerminalUnknown,
		observedAt,
	)
	if err != nil {
		return xerrors.Errorf("invalid Kimi unavailable observation: %w", err)
	}
	transition, err := u.repository.Record(ctx, observation)
	if err != nil {
		return xerrors.Errorf("failed to record Kimi unavailable observation: %w", err)
	}
	countKimiUsageTransition(result, transition)
	if transition == model.UsageObservationTransitionApplied {
		result.Unavailable++
	}
	return nil
}

func kimiUsageObservation(
	sessionID types.SessionID,
	sample application.KimiUsageSample,
) (*model.UsageObservation, error) {
	recordID := strings.TrimSpace(sample.RecordID)
	if !validKimiUsageIdentity(recordID) {
		return nil, xerrors.Errorf("invalid Kimi usage record identity")
	}
	digest := sha256.Sum256([]byte(recordID))
	id, err := types.UsageObservationIDFrom("kimi:main_wire:" + hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, xerrors.Errorf("invalid Kimi usage identity: %w", err)
	}
	source, err := types.UsageSourceOf(
		"kimi", sample.SourceName, sample.SourceVersion, "moonshot", sample.Model,
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Kimi usage source: %w", err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		id,
		sessionID,
		source,
		types.UsageScopeCall,
		types.UsageAccountingExcluded,
		sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Kimi usage descriptor: %w", err)
	}
	counters, err := kimiUsageCounters(sample.Counters)
	if err != nil {
		return nil, err
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor,
		counters,
		types.UnavailableUsageCost(),
		types.UsageTerminalUnknown,
		sample.ObservedAt.UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("invalid Kimi usage observation: %w", err)
	}
	return observation, nil
}

func kimiUsageCounters(raw application.KimiUsageCounters) (types.UsageCounters, error) {
	values := make([]types.UsageValue, 0, 4)
	for _, value := range []*int64{
		raw.InputOther, raw.InputCacheRead, raw.InputCacheCreation, raw.Output,
	} {
		if value == nil {
			values = append(values, types.UnavailableUsageValue())
			continue
		}
		known, err := types.KnownUsageValue(*value)
		if err != nil {
			return types.UsageCounters{}, xerrors.Errorf("invalid Kimi usage counter: %w", err)
		}
		values = append(values, known)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(
		values[0], values[1], values[2], values[3], unavailable, unavailable,
	)
	if err != nil {
		return types.UsageCounters{}, xerrors.Errorf("invalid Kimi usage counters: %w", err)
	}
	return counters, nil
}

func countKimiUsageTransition(
	result *KimiUsageCaptureResult,
	transition model.UsageObservationTransition,
) {
	switch transition {
	case model.UsageObservationTransitionApplied:
		result.Applied++
	case model.UsageObservationTransitionAlreadyApplied:
		result.AlreadyApplied++
	}
}

func validKimiUsageIdentity(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00")
}
