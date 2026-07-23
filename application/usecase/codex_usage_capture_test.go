package usecase_test

import (
	"context"
	"errors"
	"strings"
	"sync"
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
	claims       map[types.UsageExclusivityKey]types.UsageObservationID
	recordCalls  int
	mu           sync.Mutex
}

func (r *codexUsageRepositoryFake) Record(_ context.Context, proposed *model.UsageObservation) (model.UsageObservationTransition, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.record(proposed)
}

func (r *codexUsageRepositoryFake) record(proposed *model.UsageObservation) (model.UsageObservationTransition, error) {
	r.recordCalls++
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
	r.mu.Lock()
	defer r.mu.Unlock()
	observation := r.observations[id]
	if observation == nil {
		return types.None[*model.UsageObservation](), nil
	}
	return types.Some(observation), nil
}

func (r *codexUsageRepositoryFake) RecordExclusive(
	_ context.Context,
	key types.UsageExclusivityKey,
	additive, excluded *model.UsageObservation,
) (model.UsageObservationTransition, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := additive.ValidateAccountingAlternative(excluded); err != nil {
		return "", xerrors.Errorf("invalid fake accounting alternatives: %w", err)
	}
	if r.claims == nil {
		r.claims = map[types.UsageExclusivityKey]types.UsageObservationID{}
	}
	selected := additive
	additiveID := additive.Descriptor().ObservationID()
	if winner, present := r.claims[key]; present && winner != additiveID {
		selected = excluded
	} else if !present {
		r.claims[key] = additiveID
	}
	return r.record(selected)
}

func TestCodexUsageCaptureUsecase_RecordsKnownAndUnavailableFieldsOnce(t *testing.T) {
	t.Parallel()
	knownZero, knownInput, knownCached, knownOutput, knownReasoning, knownTotal := int64(0), int64(120), int64(80), int64(7), int64(3), int64(127)
	observedAt := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	source := codexUsageSourceStub{result: application.CodexUsageLoadResult{BoundaryObserved: true, Samples: []application.CodexUsageSample{{
		RecordID: "rollout_jsonl:file:9", SourceName: "rollout_jsonl", SourceVersion: "0.145.0", Model: "gpt-5.6-sol", ObservedAt: observedAt,
		TerminalCode: types.UsageTerminalSuccess, Available: true,
		Counters: application.CodexUsageCounters{
			InputTokens: &knownInput, CachedInputTokens: &knownCached, CacheWriteInputTokens: &knownZero,
			OutputTokens: &knownOutput, ReasoningOutputTokens: &knownReasoning, TotalTokens: &knownTotal,
		},
	}}}}
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewCodexUsageCaptureUsecase(source, repository)
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
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewCodexUsageCaptureUsecase(codexUsageSourceStub{}, repository)
	input := usecase.CodexUsageCaptureInput{SessionID: types.SessionID("session-1"), DeliveryID: "event_id:stop-missing"}

	first, err := sut.Capture(context.Background(), input)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	second, err := sut.Capture(context.Background(), input)
	if err != nil {
		t.Fatalf("Capture(replay) error = %v", err)
	}
	if first.Applied != 1 || first.Unavailable != 1 || second.AlreadyApplied != 1 || len(repository.observations) != 1 || repository.recordCalls != 2 {
		t.Fatalf("results = first %+v second %+v rows=%d", first, second, len(repository.observations))
	}
	for _, observation := range repository.observations {
		terminal, present := observation.TerminalCode().Value()
		if observation.Counters().Availability() != types.UsageAvailabilityUnavailable || observation.Descriptor().Accounting() != types.UsageAccountingExcluded || !present || terminal != types.UsageTerminalUnknown {
			t.Fatalf("unavailable observation = availability %s accounting %s", observation.Counters().Availability(), observation.Descriptor().Accounting())
		}
	}
}

func TestCodexUsageCaptureUsecase_RejectsChangedUnavailableBoundaryThroughDomainReconcile(t *testing.T) {
	t.Parallel()
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewCodexUsageCaptureUsecase(codexUsageSourceStub{}, repository)
	input := usecase.CodexUsageCaptureInput{
		SessionID: "session-1", DeliveryID: "event_id:stop-missing",
		FallbackTerminal: types.UsageTerminalSuccess,
	}
	if _, err := sut.Capture(context.Background(), input); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	input.FallbackTerminal = types.UsageTerminalFailure
	_, err := sut.Capture(context.Background(), input)
	if err == nil || !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("Capture(changed terminal) error = %v", err)
	}
	if repository.recordCalls != 2 {
		t.Fatalf("Record() calls = %d, want 2", repository.recordCalls)
	}
}

func TestCodexUsageCaptureUsecase_MissingUsageWithoutStableDeliveryDoesNotInventIdentity(t *testing.T) {
	t.Parallel()
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewCodexUsageCaptureUsecase(codexUsageSourceStub{}, repository)
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
	source := codexUsageSourceStub{result: application.CodexUsageLoadResult{BoundaryObserved: true, Samples: []application.CodexUsageSample{{
		RecordID: "rollout_jsonl:file:1", SourceName: "rollout_jsonl", SourceVersion: "1", ObservedAt: time.Now(),
		TerminalCode: types.UsageTerminalSuccess, Available: true,
		Counters: application.CodexUsageCounters{InputTokens: &negative},
	}}}}
	_, err := usecase.NewCodexUsageCaptureUsecase(source, &codexUsageRepositoryFake{}).Capture(
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
		return codexUsageSourceStub{result: application.CodexUsageLoadResult{BoundaryObserved: true, Samples: []application.CodexUsageSample{{
			RecordID: "rollout_jsonl:file:1", SourceName: "rollout_jsonl", SourceVersion: "1", ObservedAt: observedAt,
			TerminalCode: types.UsageTerminalSuccess, Available: true,
			Counters: application.CodexUsageCounters{InputTokens: value},
		}}}}
	}
	if _, err := usecase.NewCodexUsageCaptureUsecase(makeSource(&firstValue), repository).Capture(context.Background(), input); err != nil {
		t.Fatalf("Capture(first) error = %v", err)
	}
	_, err := usecase.NewCodexUsageCaptureUsecase(makeSource(&changedValue), repository).Capture(context.Background(), input)
	if err == nil || !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("Capture(changed replay) error = %v", err)
	}
}

func TestCodexUsageCaptureUsecase_HeadlessObservationSuppressesLegacyRolloutAccounting(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	inputTokens, outputTokens := int64(10), int64(2)
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewCodexUsageCaptureUsecase(codexUsageSourceStub{}, repository)
	headless := application.CodexUsageLoadResult{BoundaryObserved: true, Samples: []application.CodexUsageSample{{
		RecordID: "headless_stream:thread-1:1", SuppressionID: "headless_stream:thread-1:1",
		SourceName: "headless_stream", SourceVersion: "schema-v1",
		ObservedAt: observedAt, TerminalCode: types.UsageTerminalSuccess, Available: true,
		Counters: application.CodexUsageCounters{InputTokens: &inputTokens, OutputTokens: &outputTokens},
	}}}
	if _, err := sut.CaptureHeadless(context.Background(), usecase.CodexUsageCaptureInput{SessionID: "session-1"}, headless); err != nil {
		t.Fatalf("CaptureHeadless() error = %v", err)
	}
	rolloutSource := codexUsageSourceStub{result: application.CodexUsageLoadResult{BoundaryObserved: true, Samples: []application.CodexUsageSample{{
		RecordID: "rollout:thread-1:turn-1", SuppressionID: "headless_stream:thread-1:1",
		SourceName: "rollout_jsonl", SourceVersion: "0.145.0", ObservedAt: observedAt,
		TerminalCode: types.UsageTerminalSuccess, Available: true,
		Counters: application.CodexUsageCounters{InputTokens: &inputTokens, OutputTokens: &outputTokens},
	}}}}
	if _, err := usecase.NewCodexUsageCaptureUsecase(rolloutSource, repository).Capture(context.Background(), usecase.CodexUsageCaptureInput{SessionID: "session-1"}); err != nil {
		t.Fatalf("Capture(rollout) error = %v", err)
	}
	rollout := repository.observations["codex:rollout:thread-1:turn-1"]
	if rollout == nil || rollout.Descriptor().Accounting() != types.UsageAccountingExcluded {
		t.Fatalf("rollout accounting = %+v", rollout)
	}
}

func TestCodexUsageCaptureUsecase_RolloutObservationSuppressesLaterHeadlessAccounting(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	inputTokens, outputTokens := int64(10), int64(2)
	repository := &codexUsageRepositoryFake{}
	rolloutSource := codexUsageSourceStub{result: application.CodexUsageLoadResult{BoundaryObserved: true, Samples: []application.CodexUsageSample{{
		RecordID: "rollout:thread-1:turn-1", SuppressionID: "headless_stream:thread-1:1",
		SourceName: "rollout_jsonl", SourceVersion: "0.145.0", ObservedAt: observedAt,
		TerminalCode: types.UsageTerminalSuccess, Available: true,
		Counters: application.CodexUsageCounters{InputTokens: &inputTokens, OutputTokens: &outputTokens},
	}}}}
	input := usecase.CodexUsageCaptureInput{SessionID: "session-1"}
	if _, err := usecase.NewCodexUsageCaptureUsecase(rolloutSource, repository).Capture(context.Background(), input); err != nil {
		t.Fatalf("Capture(rollout) error = %v", err)
	}
	headless := application.CodexUsageLoadResult{BoundaryObserved: true, Samples: []application.CodexUsageSample{{
		RecordID: "headless_stream:thread-1:1", SuppressionID: "headless_stream:thread-1:1",
		SourceName: "headless_stream", SourceVersion: "schema-v1", ObservedAt: observedAt,
		TerminalCode: types.UsageTerminalSuccess, Available: true,
		Counters: application.CodexUsageCounters{InputTokens: &inputTokens, OutputTokens: &outputTokens},
	}}}
	sut := usecase.NewCodexUsageCaptureUsecase(codexUsageSourceStub{}, repository)
	if _, err := sut.CaptureHeadless(context.Background(), input, headless); err != nil {
		t.Fatalf("CaptureHeadless() error = %v", err)
	}
	rollout := repository.observations["codex:rollout:thread-1:turn-1"]
	headlessObservation := repository.observations["codex:headless_stream:thread-1:1"]
	if rollout == nil || rollout.Descriptor().Accounting() != types.UsageAccountingAdditive {
		t.Fatalf("rollout accounting = %+v", rollout)
	}
	if headlessObservation == nil || headlessObservation.Descriptor().Accounting() != types.UsageAccountingExcluded {
		t.Fatalf("headless accounting = %+v", headlessObservation)
	}
	if len(repository.claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(repository.claims))
	}
}
