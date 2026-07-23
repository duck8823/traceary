package usecase_test

import (
	"context"
	"io"
	"testing"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type googleUsageRepositoryStub struct {
	observations map[types.UsageObservationID]*model.UsageObservation
	heads        map[string]*model.UsageObservation
}

func newGoogleUsageRepositoryStub() *googleUsageRepositoryStub {
	return &googleUsageRepositoryStub{
		observations: map[types.UsageObservationID]*model.UsageObservation{},
		heads:        map[string]*model.UsageObservation{},
	}
}

func (s *googleUsageRepositoryStub) Record(
	_ context.Context,
	observation *model.UsageObservation,
) (model.UsageObservationTransition, error) {
	id := observation.Descriptor().ObservationID()
	if existing := s.observations[id]; existing != nil {
		transition, err := existing.Reconcile(observation)
		if err != nil {
			return "", xerrors.Errorf("reconcile usage observation: %w", err)
		}
		return transition, nil
	}
	if observation.Descriptor().Scope() == types.UsageScopeSessionSnapshot {
		head := s.heads[observation.Descriptor().SnapshotSeries()]
		if err := observation.ValidateSnapshotSuccessor(head); err != nil {
			return "", xerrors.Errorf("validate snapshot successor: %w", err)
		}
		s.heads[observation.Descriptor().SnapshotSeries()] = observation
	}
	s.observations[id] = observation
	return model.UsageObservationTransitionApplied, nil
}

func (s *googleUsageRepositoryStub) FindByID(
	_ context.Context,
	id types.UsageObservationID,
) (types.Optional[*model.UsageObservation], error) {
	if observation := s.observations[id]; observation != nil {
		return types.Some(observation), nil
	}
	return types.None[*model.UsageObservation](), nil
}

func (s *googleUsageRepositoryStub) FindSnapshotHead(
	_ context.Context,
	series string,
) (types.Optional[*model.UsageObservation], error) {
	if observation := s.heads[series]; observation != nil {
		return types.Some(observation), nil
	}
	return types.None[*model.UsageObservation](), nil
}

func TestGeminiUsageCapture_RecordsModelRunsWithoutAggregateDuplicate(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGeminiUsageCaptureUsecase(repository)
	at := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	loaded := application.GeminiUsageLoadResult{
		BoundaryObserved: true,
		Samples: []application.GeminiUsageSample{
			geminiUsageSampleFixture("record-a", "model-a", at, 14, 6, 4, 20),
			geminiUsageSampleFixture("record-b", "model-b", at, 7, 3, 3, 10),
		},
	}
	input := usecase.GeminiUsageCaptureInput{SessionID: "session-1", DeliveryID: "session_run"}
	result, err := capture.CaptureHeadless(context.Background(), input, loaded)
	if err != nil {
		t.Fatalf("CaptureHeadless() error = %v", err)
	}
	if result.Applied != 2 || len(repository.observations) != 2 {
		t.Fatalf("result = %+v observations = %d", result, len(repository.observations))
	}
	result, err = capture.CaptureHeadless(context.Background(), input, loaded)
	if err != nil || result.AlreadyApplied != 2 || len(repository.observations) != 2 {
		t.Fatalf("redelivery = (%+v, %v), observations = %d", result, err, len(repository.observations))
	}
	secondRun, err := capture.CaptureHeadless(
		context.Background(),
		usecase.GeminiUsageCaptureInput{SessionID: "session-2", DeliveryID: "session_run"},
		loaded,
	)
	if err != nil || secondRun.Applied != 2 || len(repository.observations) != 4 {
		t.Fatalf("second run = (%+v, %v), observations = %d", secondRun, err, len(repository.observations))
	}
}

func TestGeminiUsageCapture_RecordsUnavailableRunAndInteractiveCall(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGeminiUsageCaptureUsecase(repository)
	headless, err := capture.CaptureHeadless(
		context.Background(),
		usecase.GeminiUsageCaptureInput{
			SessionID: "session-1", DeliveryID: "session_run",
			FallbackTerminal: types.UsageTerminalFailure,
		},
		application.GeminiUsageLoadResult{},
	)
	if err != nil || headless.Unavailable != 1 {
		t.Fatalf("CaptureHeadless() = (%+v, %v)", headless, err)
	}
	interactive, err := capture.CaptureInteractiveUnavailable(
		context.Background(),
		usecase.GeminiUsageCaptureInput{SessionID: "session-1", DeliveryID: "timestamp:2026-07-23T01:00:00Z"},
	)
	if err != nil || interactive.Unavailable != 1 {
		t.Fatalf("CaptureInteractiveUnavailable() = (%+v, %v)", interactive, err)
	}
	var runCount, callCount int
	for _, observation := range repository.observations {
		switch observation.Descriptor().Scope() {
		case types.UsageScopeRun:
			runCount++
		case types.UsageScopeCall:
			callCount++
		}
		if observation.Counters().Availability() != types.UsageAvailabilityUnavailable {
			t.Fatalf("availability = %q", observation.Counters().Availability())
		}
	}
	if runCount != 1 || callCount != 1 {
		t.Fatalf("run = %d call = %d", runCount, callCount)
	}
}

func TestGeminiUsageCapture_RejectsInconsistentTerminalBoundary(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGeminiUsageCaptureUsecase(repository)
	input := usecase.GeminiUsageCaptureInput{SessionID: "session-1", DeliveryID: "session_run"}
	at := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	for name, loaded := range map[string]application.GeminiUsageLoadResult{
		"sample without boundary": {
			Samples: []application.GeminiUsageSample{
				geminiUsageSampleFixture("record-a", "model-a", at, 1, 1, 0, 2),
			},
		},
		"boundary without sample": {BoundaryObserved: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := capture.CaptureHeadless(context.Background(), input, loaded); err == nil {
				t.Fatal("CaptureHeadless() error = nil")
			}
			if len(repository.observations) != 0 {
				t.Fatalf("observations = %d", len(repository.observations))
			}
		})
	}
}

type antigravityUsageSourceStub struct {
	snapshot *application.AntigravityUsageSnapshot
}

func (s *antigravityUsageSourceStub) Decode(
	context.Context,
	io.Reader,
) (*application.AntigravityUsageSnapshot, error) {
	if s.snapshot == nil {
		return nil, nil
	}
	snapshotCopy := *s.snapshot
	return &snapshotCopy, nil
}

func TestAntigravityUsageCapture_SupersedesOnlyMonotonicIdleSnapshots(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	source := &antigravityUsageSourceStub{snapshot: &application.AntigravityUsageSnapshot{
		ConversationID: "conversation-1",
		Model:          "model-1",
		SourceVersion:  "1.1.5",
		ObservedAt:     time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC),
		InputTokens:    100,
		OutputTokens:   20,
	}}
	capture := usecase.NewAntigravityUsageCaptureUsecase(source, repository)
	result, err := capture.CaptureStatus(context.Background(), nil)
	if err != nil || result.Applied != 1 {
		t.Fatalf("first CaptureStatus() = (%+v, %v)", result, err)
	}
	result, err = capture.CaptureStatus(context.Background(), nil)
	if err != nil || result.AlreadyApplied != 1 || len(repository.observations) != 1 {
		t.Fatalf("redelivery = (%+v, %v), observations = %d", result, err, len(repository.observations))
	}
	source.snapshot.ObservedAt = source.snapshot.ObservedAt.Add(time.Minute)
	source.snapshot.InputTokens = 130
	source.snapshot.OutputTokens = 25
	result, err = capture.CaptureStatus(context.Background(), nil)
	if err != nil || result.Applied != 1 || len(repository.observations) != 2 {
		t.Fatalf("successor = (%+v, %v), observations = %d", result, err, len(repository.observations))
	}
	var head *model.UsageObservation
	for _, value := range repository.heads {
		head = value
	}
	if head == nil || head.Descriptor().SnapshotRevision() != 2 {
		t.Fatalf("head = %+v", head)
	}
	if predecessor, present := head.Descriptor().SupersedesID().Value(); !present ||
		repository.observations[predecessor] == nil {
		t.Fatalf("predecessor = (%q, %t)", predecessor, present)
	}

	source.snapshot.InputTokens = 129
	if _, err := capture.CaptureStatus(context.Background(), nil); err == nil ||
		len(repository.observations) != 2 {
		t.Fatalf("regression error = %v observations = %d", err, len(repository.observations))
	}

	source.snapshot.InputTokens = 140
	source.snapshot.OutputTokens = 30
	source.snapshot.SourceVersion = "1.1.6"
	if _, err := capture.CaptureStatus(context.Background(), nil); err == nil ||
		len(repository.observations) != 2 {
		t.Fatalf("cross-version successor error = %v observations = %d", err, len(repository.observations))
	}
}

func TestAntigravityUsageCapture_RecordsUnavailableStopBoundaryIdempotently(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewAntigravityUsageCaptureUsecase(&antigravityUsageSourceStub{}, repository)
	input := usecase.AntigravityUsageStopInput{
		SessionID: "conversation-1", BoundaryID: "turn:step-1",
	}
	first, err := capture.CaptureStopUnavailable(context.Background(), input)
	if err != nil || first.Applied != 1 || first.Unavailable != 1 {
		t.Fatalf("first = (%+v, %v)", first, err)
	}
	second, err := capture.CaptureStopUnavailable(context.Background(), input)
	if err != nil || second.AlreadyApplied != 1 || second.Unavailable != 0 ||
		len(repository.observations) != 1 {
		t.Fatalf("second = (%+v, %v), observations = %d", second, err, len(repository.observations))
	}
}

func geminiUsageSampleFixture(
	recordID, modelName string,
	at time.Time,
	input, output, cached, total int64,
) application.GeminiUsageSample {
	return application.GeminiUsageSample{
		RecordID: recordID, SourceName: "headless_stream", SourceVersion: "schema-v1",
		Model: modelName, ObservedAt: at, TerminalCode: types.UsageTerminalSuccess, Available: true,
		Counters: application.GeminiUsageCounters{
			InputTokens: &input, CachedInputTokens: &cached,
			OutputTokens: &output, TotalTokens: &total,
		},
	}
}
