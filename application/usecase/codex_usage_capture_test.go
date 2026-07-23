package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type codexUsageSourceStub struct {
	result application.CodexUsageLoadResult
	err    error
}

func (s codexUsageSourceStub) Load(context.Context, application.CodexUsageLoadCriteria) (application.CodexUsageLoadResult, error) {
	return s.result, s.err
}

type codexUsageRepositoryFake struct {
	observations map[types.UsageObservationID]*model.UsageObservation
}

func (r *codexUsageRepositoryFake) Record(_ context.Context, proposed *model.UsageObservation) (model.UsageObservationTransition, error) {
	if r.observations == nil {
		r.observations = map[types.UsageObservationID]*model.UsageObservation{}
	}
	id := proposed.Descriptor().ObservationID()
	current := r.observations[id]
	if current == nil {
		r.observations[id] = proposed
		return model.UsageObservationTransitionApplied, nil
	}
	transition, err := current.Reconcile(proposed)
	if err != nil {
		return "", xerrors.Errorf("failed to reconcile fake Codex usage: %w", err)
	}
	r.observations[id] = current
	return transition, nil
}

func (r *codexUsageRepositoryFake) FindByID(_ context.Context, id types.UsageObservationID) (types.Optional[*model.UsageObservation], error) {
	observation := r.observations[id]
	if observation == nil {
		return types.None[*model.UsageObservation](), nil
	}
	return types.Some(observation), nil
}

type fixedCodexUsageClock struct{ now time.Time }

func (c fixedCodexUsageClock) Now() time.Time { return c.now }

func TestCodexUsageCaptureUsecase_RecordsKnownAndUnavailableFieldsOnce(t *testing.T) {
	t.Parallel()
	knownZero, knownInput, knownCached, knownOutput, knownReasoning, knownTotal := int64(0), int64(120), int64(80), int64(7), int64(3), int64(127)
	observedAt := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	source := codexUsageSourceStub{result: application.CodexUsageLoadResult{Samples: []application.CodexUsageSample{{
		RecordID: "rollout_jsonl:file:9", SourceName: "rollout_jsonl", SourceVersion: "0.145.0", Model: "gpt-5.6-sol", ObservedAt: observedAt,
		Counters: application.CodexUsageCounters{
			InputTokens: &knownInput, CachedInputTokens: &knownCached, CacheWriteInputTokens: &knownZero,
			OutputTokens: &knownOutput, ReasoningOutputTokens: &knownReasoning, TotalTokens: &knownTotal,
		},
	}}}}
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewCodexUsageCaptureUsecase(source, repository, fixedCodexUsageClock{now: observedAt})
	input := usecase.CodexUsageCaptureInput{SessionID: types.SessionID("session-1"), DeliveryID: "event_id:stop-1"}

	first, err := sut.Capture(context.Background(), input)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	second, err := sut.Capture(context.Background(), input)
	if err != nil {
		t.Fatalf("Capture(replay) error = %v", err)
	}
	if first.Applied != 1 || first.AlreadyApplied != 0 || second.AlreadyApplied != 1 || len(repository.observations) != 1 {
		t.Fatalf("results = first %+v second %+v rows=%d", first, second, len(repository.observations))
	}
	for _, observation := range repository.observations {
		counters := observation.Counters()
		if value, present := counters.CacheWriteInput().Value(); !present || value != 0 {
			t.Fatalf("cache write = %d/%t, want known zero", value, present)
		}
		if observation.Cost().State() != types.UsageCostUnavailable || observation.Descriptor().Source().Model() != "gpt-5.6-sol" {
			t.Fatalf("observation = cost %s model %q", observation.Cost().State(), observation.Descriptor().Source().Model())
		}
	}
}

func TestCodexUsageCaptureUsecase_RecordsStableUnavailableBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 23, 4, 5, 6, 0, time.UTC)
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewCodexUsageCaptureUsecase(codexUsageSourceStub{}, repository, fixedCodexUsageClock{now: now})
	input := usecase.CodexUsageCaptureInput{SessionID: types.SessionID("session-1"), DeliveryID: "event_id:stop-missing"}

	first, err := sut.Capture(context.Background(), input)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	second, err := sut.Capture(context.Background(), input)
	if err != nil {
		t.Fatalf("Capture(replay) error = %v", err)
	}
	if first.Applied != 1 || first.Unavailable != 1 || second.AlreadyApplied != 1 || len(repository.observations) != 1 {
		t.Fatalf("results = first %+v second %+v rows=%d", first, second, len(repository.observations))
	}
	for _, observation := range repository.observations {
		terminal, present := observation.TerminalCode().Value()
		if observation.Counters().Availability() != types.UsageAvailabilityUnavailable || observation.Descriptor().Accounting() != types.UsageAccountingExcluded || !present || terminal != types.UsageTerminalUnknown {
			t.Fatalf("unavailable observation = availability %s accounting %s", observation.Counters().Availability(), observation.Descriptor().Accounting())
		}
	}
}

func TestCodexUsageCaptureUsecase_MissingUsageWithoutStableDeliveryDoesNotInventIdentity(t *testing.T) {
	t.Parallel()
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewCodexUsageCaptureUsecase(codexUsageSourceStub{}, repository, fixedCodexUsageClock{now: time.Now()})
	result, err := sut.Capture(context.Background(), usecase.CodexUsageCaptureInput{SessionID: types.SessionID("session-1")})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if result != (usecase.CodexUsageCaptureResult{}) || len(repository.observations) != 0 {
		t.Fatalf("result = %+v rows=%d", result, len(repository.observations))
	}
}

func TestCodexUsageCaptureUsecase_RejectsInvalidSourceCounter(t *testing.T) {
	t.Parallel()
	negative := int64(-1)
	source := codexUsageSourceStub{result: application.CodexUsageLoadResult{Samples: []application.CodexUsageSample{{
		RecordID: "rollout_jsonl:file:1", SourceName: "rollout_jsonl", SourceVersion: "1", ObservedAt: time.Now(),
		Counters: application.CodexUsageCounters{InputTokens: &negative},
	}}}}
	_, err := usecase.NewCodexUsageCaptureUsecase(source, &codexUsageRepositoryFake{}, nil).Capture(
		context.Background(), usecase.CodexUsageCaptureInput{SessionID: types.SessionID("session-1")},
	)
	if err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("Capture() error = %v", err)
	}
}

func TestCodexUsageCaptureUsecase_RejectsChangedReplaySemantics(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	firstValue, changedValue := int64(10), int64(11)
	repository := &codexUsageRepositoryFake{}
	input := usecase.CodexUsageCaptureInput{SessionID: types.SessionID("session-1")}
	makeSource := func(value *int64) codexUsageSourceStub {
		return codexUsageSourceStub{result: application.CodexUsageLoadResult{Samples: []application.CodexUsageSample{{
			RecordID: "rollout_jsonl:file:1", SourceName: "rollout_jsonl", SourceVersion: "1", ObservedAt: observedAt,
			Counters: application.CodexUsageCounters{InputTokens: value},
		}}}}
	}
	if _, err := usecase.NewCodexUsageCaptureUsecase(makeSource(&firstValue), repository, nil).Capture(context.Background(), input); err != nil {
		t.Fatalf("Capture(first) error = %v", err)
	}
	_, err := usecase.NewCodexUsageCaptureUsecase(makeSource(&changedValue), repository, nil).Capture(context.Background(), input)
	if err == nil || !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("Capture(changed replay) error = %v", err)
	}
}
