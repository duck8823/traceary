package usecase_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

func assertUnavailableObservationNotEpoch(t *testing.T, observation *model.UsageObservation) {
	t.Helper()
	if observation == nil {
		t.Fatal("observation is nil")
	}
	epoch := time.Unix(0, 0).UTC()
	observedAt := observation.Descriptor().ObservedAt()
	if !observedAt.After(epoch) {
		t.Fatalf("observed_at = %s, want after Unix epoch", observedAt)
	}
	finalizedAt, present := observation.FinalizedAt().Value()
	if !present || !finalizedAt.After(epoch) {
		t.Fatalf("finalized_at = (%s, %t), want after Unix epoch", finalizedAt, present)
	}
}

func TestGeminiUsageCapture_PrefersNonEpochEventTime(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGeminiUsageCaptureUsecase(repository)
	eventTime := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	result, err := capture.CaptureInteractiveUnavailable(
		t.Context(),
		usecase.GeminiUsageCaptureInput{
			SessionID:  "session-1",
			DeliveryID: "timestamp:2026-08-16T12:00:00Z",
			EventTime:  eventTime,
		},
	)
	if err != nil || result.Unavailable != 1 {
		t.Fatalf("CaptureInteractiveUnavailable() = (%+v, %v)", result, err)
	}
	for _, observation := range repository.observations {
		if !observation.Descriptor().ObservedAt().Equal(eventTime) {
			t.Fatalf("observed_at = %s, want event time %s", observation.Descriptor().ObservedAt(), eventTime)
		}
		assertUnavailableObservationNotEpoch(t, observation)
	}
	replayed, err := capture.CaptureInteractiveUnavailable(
		t.Context(),
		usecase.GeminiUsageCaptureInput{
			SessionID:  "session-1",
			DeliveryID: "timestamp:2026-08-16T12:00:00Z",
			EventTime:  eventTime.Add(time.Minute),
		},
	)
	if err != nil || replayed.AlreadyApplied != 1 {
		t.Fatalf("replay = (%+v, %v)", replayed, err)
	}
	for _, observation := range repository.observations {
		if !observation.Descriptor().ObservedAt().Equal(eventTime) {
			t.Fatalf("replay mutated observed_at = %s", observation.Descriptor().ObservedAt())
		}
	}
}

func TestGeminiUsageCapture_TreatsEpochEventTimeAsMissing(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGeminiUsageCaptureUsecase(repository)
	result, err := capture.CaptureInteractiveUnavailable(
		t.Context(),
		usecase.GeminiUsageCaptureInput{
			SessionID:  "session-1",
			DeliveryID: "delivery-1",
			EventTime:  time.Unix(0, 0).UTC(),
		},
	)
	if err != nil || result.Unavailable != 1 {
		t.Fatalf("CaptureInteractiveUnavailable() = (%+v, %v)", result, err)
	}
	for _, observation := range repository.observations {
		assertUnavailableObservationNotEpoch(t, observation)
	}
}

func TestGrokUsageCapture_ReusesFirstWriteTimeOnReplay(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGrokUsageCaptureUsecase(repository)
	input := usecase.GrokUsageCaptureInput{SessionID: "session-1", DeliveryID: "prompt_id:prompt-1"}
	first, err := capture.CaptureHookUnavailable(t.Context(), input)
	if err != nil || first.Unavailable != 1 {
		t.Fatalf("first = (%+v, %v)", first, err)
	}
	var firstObserved time.Time
	for _, observation := range repository.observations {
		assertUnavailableObservationNotEpoch(t, observation)
		firstObserved = observation.Descriptor().ObservedAt()
	}
	time.Sleep(2 * time.Millisecond)
	replayed, err := capture.CaptureHookUnavailable(t.Context(), input)
	if err != nil || replayed.AlreadyApplied != 1 {
		t.Fatalf("replay = (%+v, %v)", replayed, err)
	}
	for _, observation := range repository.observations {
		if !observation.Descriptor().ObservedAt().Equal(firstObserved) {
			t.Fatalf("replay observed_at = %s, want %s", observation.Descriptor().ObservedAt(), firstObserved)
		}
	}
}
