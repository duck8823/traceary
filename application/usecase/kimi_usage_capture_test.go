package usecase_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type kimiUsageSourceStub struct {
	result application.KimiUsageLoadResult
	err    error
}

func (s *kimiUsageSourceStub) Load(context.Context, string) (application.KimiUsageLoadResult, error) {
	return s.result, s.err
}

type kimiUsageRepositoryStub struct {
	observations map[types.UsageObservationID]*model.UsageObservation
}

func newKimiUsageRepositoryStub() *kimiUsageRepositoryStub {
	return &kimiUsageRepositoryStub{observations: map[types.UsageObservationID]*model.UsageObservation{}}
}

func (s *kimiUsageRepositoryStub) Record(
	_ context.Context,
	observation *model.UsageObservation,
) (model.UsageObservationTransition, error) {
	id := observation.Descriptor().ObservationID()
	if current, ok := s.observations[id]; ok {
		if reflect.DeepEqual(current, observation) {
			return model.UsageObservationTransitionAlreadyApplied, nil
		}
		return "", model.ErrConflictingUsageObservation
	}
	s.observations[id] = observation
	return model.UsageObservationTransitionApplied, nil
}

func (s *kimiUsageRepositoryStub) FindByID(
	_ context.Context,
	id types.UsageObservationID,
) (types.Optional[*model.UsageObservation], error) {
	if observation, ok := s.observations[id]; ok {
		return types.Some(observation), nil
	}
	return types.None[*model.UsageObservation](), nil
}

func (s *kimiUsageRepositoryStub) FindSnapshotHead(
	context.Context,
	types.UsageSource,
	string,
) (types.Optional[*model.UsageObservation], error) {
	return types.None[*model.UsageObservation](), nil
}

func TestKimiUsageCapture_RecordsPartialExcludedRowsAndUnavailableBoundary(t *testing.T) {
	zero, three, two, five := int64(0), int64(3), int64(2), int64(5)
	source := &kimiUsageSourceStub{result: application.KimiUsageLoadResult{
		LatestTurnOrdinal: 2,
		Samples: []application.KimiUsageSample{{
			RecordID:      "main_wire:record-a",
			SourceName:    "main_wire",
			SourceVersion: "0.29.0",
			Model:         "kimi-code/k3",
			ObservedAt:    time.UnixMilli(1784466740000).UTC(),
			Counters: application.KimiUsageCounters{
				InputOther:         &zero,
				InputCacheRead:     &three,
				InputCacheCreation: &two,
				Output:             &five,
			},
		}},
	}}
	repository := newKimiUsageRepositoryStub()
	capture := usecase.NewKimiUsageCaptureUsecase(source, repository)
	input := usecase.KimiUsageCaptureInput{
		SessionID:         types.SessionID("provider-session"),
		ProviderSessionID: "provider-session",
		Boundary:          usecase.KimiUsageBoundaryStop,
	}

	first, err := capture.Capture(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := capture.Capture(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Applied != 2 || first.Unavailable != 1 || second.AlreadyApplied != 2 {
		t.Fatalf("first/second = %+v / %+v", first, second)
	}
	if len(repository.observations) != 2 {
		t.Fatalf("observations = %d, want source and boundary rows", len(repository.observations))
	}
	for _, observation := range repository.observations {
		descriptor := observation.Descriptor()
		if descriptor.Accounting() != types.UsageAccountingExcluded || descriptor.Scope() != types.UsageScopeCall {
			t.Fatalf("descriptor = %+v", descriptor)
		}
		if descriptor.Source().Name() == "main_wire" {
			counters := observation.Counters()
			if value, known := counters.Input().Value(); !known || value != 0 {
				t.Fatalf("known zero input was not preserved: %+v", counters.Input())
			}
			if value, known := counters.CachedInput().Value(); !known || value != 3 {
				t.Fatalf("cached input = %+v", counters.CachedInput())
			}
			if value, known := counters.CacheWriteInput().Value(); !known || value != 2 {
				t.Fatalf("cache creation = %+v", counters.CacheWriteInput())
			}
			if counters.Total().State() != types.UsageValueUnavailable {
				t.Fatalf("total must not be inferred: %+v", counters.Total())
			}
		}
	}
}

func TestKimiUsageCapture_SeparatesStopAndSessionEndAvailability(t *testing.T) {
	source := &kimiUsageSourceStub{result: application.KimiUsageLoadResult{LatestTurnOrdinal: 4}}
	repository := newKimiUsageRepositoryStub()
	capture := usecase.NewKimiUsageCaptureUsecase(source, repository)
	for _, boundary := range []usecase.KimiUsageBoundary{
		usecase.KimiUsageBoundaryStop,
		usecase.KimiUsageBoundarySessionEnd,
	} {
		result, err := capture.Capture(context.Background(), usecase.KimiUsageCaptureInput{
			SessionID:         types.SessionID("provider-session"),
			ProviderSessionID: "provider-session",
			Boundary:          boundary,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Applied != 1 || result.Unavailable != 1 {
			t.Fatalf("%s result = %+v", boundary, result)
		}
	}
	if len(repository.observations) != 2 {
		t.Fatalf("boundary observations = %d, want 2", len(repository.observations))
	}
}

func TestKimiUsageCapture_ChangedSourceRecordFailsClosed(t *testing.T) {
	value := int64(1)
	source := &kimiUsageSourceStub{result: application.KimiUsageLoadResult{
		LatestTurnOrdinal: 1,
		Samples: []application.KimiUsageSample{{
			RecordID:      "main_wire:record-a",
			SourceName:    "main_wire",
			SourceVersion: "0.29.0",
			Model:         "kimi-code/k3",
			ObservedAt:    time.UnixMilli(1784466740000).UTC(),
			Counters:      application.KimiUsageCounters{InputOther: &value},
		}},
	}}
	repository := newKimiUsageRepositoryStub()
	capture := usecase.NewKimiUsageCaptureUsecase(source, repository)
	input := usecase.KimiUsageCaptureInput{
		SessionID:         types.SessionID("provider-session"),
		ProviderSessionID: "provider-session",
		Boundary:          usecase.KimiUsageBoundaryStop,
	}
	if _, err := capture.Capture(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	changed := int64(2)
	source.result.Samples[0].Counters.InputOther = &changed
	if _, err := capture.Capture(context.Background(), input); !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("error = %v, want conflict", err)
	}
}
