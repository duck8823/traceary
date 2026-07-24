package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func TestGrokUsageCapture_RecordsVerifiedRunExactlyOnce(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGrokUsageCaptureUsecase(repository)
	input := usecase.GrokUsageCaptureInput{SessionID: "session-1", DeliveryID: "session_run"}
	loaded := application.GrokUsageLoadResult{
		BoundaryObserved: true,
		Samples:          []application.GrokUsageSample{grokUsageSampleFixture()},
	}
	result, err := capture.CaptureHeadless(context.Background(), input, loaded)
	if err != nil || result.Applied != 1 || len(repository.observations) != 1 {
		t.Fatalf("CaptureHeadless() = (%+v, %v), observations = %d", result, err, len(repository.observations))
	}
	result, err = capture.CaptureHeadless(context.Background(), input, loaded)
	if err != nil || result.AlreadyApplied != 1 || len(repository.observations) != 1 {
		t.Fatalf("redelivery = (%+v, %v), observations = %d", result, err, len(repository.observations))
	}
	for _, observation := range repository.observations {
		if observation.Descriptor().Scope() != types.UsageScopeRun ||
			observation.Descriptor().Accounting() != types.UsageAccountingAdditive ||
			observation.Counters().Availability() != types.UsageAvailabilityPartial {
			t.Fatalf("observation = %+v", observation)
		}
		if value, known := observation.Counters().ReasoningOutput().Value(); !known || value != 20 {
			t.Fatalf("reasoning = (%d, %t)", value, known)
		}
		if value, known := observation.Counters().CachedInput().Value(); !known || value != 0 {
			t.Fatalf("cached input = (%d, %t)", value, known)
		}
	}
}

func TestGrokUsageCapture_DeduplicatesProviderTerminalAcrossTracearySessions(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGrokUsageCaptureUsecase(repository)
	loaded := application.GrokUsageLoadResult{
		BoundaryObserved: true,
		Samples:          []application.GrokUsageSample{grokUsageSampleFixture()},
	}
	first, err := capture.CaptureHeadless(
		context.Background(),
		usecase.GrokUsageCaptureInput{SessionID: "traceary-session-1", DeliveryID: "session_run"},
		loaded,
	)
	if err != nil || first.Applied != 1 {
		t.Fatalf("first capture = (%+v, %v)", first, err)
	}
	replayedSample := grokUsageSampleFixture()
	replayedSample.ObservedAt = replayedSample.ObservedAt.Add(time.Minute)
	loaded.Samples = []application.GrokUsageSample{replayedSample}
	replayed, err := capture.CaptureHeadless(
		context.Background(),
		usecase.GrokUsageCaptureInput{SessionID: "traceary-session-2", DeliveryID: "session_run"},
		loaded,
	)
	if err != nil || replayed.AlreadyApplied != 1 || len(repository.observations) != 1 {
		t.Fatalf("cross-session replay = (%+v, %v), observations = %d", replayed, err, len(repository.observations))
	}

	changed := grokUsageSampleFixture()
	changedTotal := *changed.Counters.TotalTokens + 1
	changed.Counters.TotalTokens = &changedTotal
	_, err = capture.CaptureHeadless(
		context.Background(),
		usecase.GrokUsageCaptureInput{SessionID: "traceary-session-3", DeliveryID: "session_run"},
		application.GrokUsageLoadResult{
			BoundaryObserved: true,
			Samples:          []application.GrokUsageSample{changed},
		},
	)
	if err == nil || len(repository.observations) != 1 {
		t.Fatalf("conflicting cross-session replay error = %v, observations = %d", err, len(repository.observations))
	}
}

func TestGrokUsageCapture_RecordsUnavailableRunAndStableHookCall(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGrokUsageCaptureUsecase(repository)
	headless, err := capture.CaptureHeadless(
		context.Background(),
		usecase.GrokUsageCaptureInput{
			SessionID: "session-1", DeliveryID: "session_run",
			FallbackTerminal: types.UsageTerminalFailure,
		},
		application.GrokUsageLoadResult{},
	)
	if err != nil || headless.Unavailable != 1 {
		t.Fatalf("CaptureHeadless() = (%+v, %v)", headless, err)
	}
	hook, err := capture.CaptureHookUnavailable(
		context.Background(),
		usecase.GrokUsageCaptureInput{SessionID: "session-1", DeliveryID: "prompt_id:prompt-1"},
	)
	if err != nil || hook.Unavailable != 1 {
		t.Fatalf("CaptureHookUnavailable() = (%+v, %v)", hook, err)
	}
	replayed, err := capture.CaptureHookUnavailable(
		context.Background(),
		usecase.GrokUsageCaptureInput{SessionID: "session-1", DeliveryID: "prompt_id:prompt-1"},
	)
	if err != nil || replayed.AlreadyApplied != 1 || replayed.Unavailable != 0 {
		t.Fatalf("hook redelivery = (%+v, %v)", replayed, err)
	}
	var runCount, callCount int
	for _, observation := range repository.observations {
		switch observation.Descriptor().Scope() {
		case types.UsageScopeRun:
			runCount++
		case types.UsageScopeCall:
			callCount++
		}
		if observation.Descriptor().Accounting() != types.UsageAccountingExcluded ||
			observation.Counters().Availability() != types.UsageAvailabilityUnavailable {
			t.Fatalf("unavailable observation = %+v", observation)
		}
	}
	if runCount != 1 || callCount != 1 {
		t.Fatalf("run = %d call = %d", runCount, callCount)
	}
}

func TestGrokUsageCapture_DeduplicatesUnavailableProviderTerminalAcrossTracearySessions(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGrokUsageCaptureUsecase(repository)
	loaded := application.GrokUsageLoadResult{
		BoundaryObserved: true,
		TerminalRecordID: "headless_stream:provider-request-without-usage",
		TerminalCode:     types.UsageTerminalFailure,
	}
	first, err := capture.CaptureHeadless(
		context.Background(),
		usecase.GrokUsageCaptureInput{SessionID: "traceary-session-1", DeliveryID: "session_run"},
		loaded,
	)
	if err != nil || first.Applied != 1 || first.Unavailable != 1 {
		t.Fatalf("first capture = (%+v, %v)", first, err)
	}
	replayed, err := capture.CaptureHeadless(
		context.Background(),
		usecase.GrokUsageCaptureInput{SessionID: "traceary-session-2", DeliveryID: "session_run"},
		loaded,
	)
	if err != nil || replayed.AlreadyApplied != 1 || replayed.Unavailable != 0 ||
		len(repository.observations) != 1 {
		t.Fatalf("cross-session replay = (%+v, %v), observations = %d", replayed, err, len(repository.observations))
	}
	for _, observation := range repository.observations {
		if observation.Descriptor().Source().Version() != "0.2.106" ||
			observation.TerminalCode().OrElse(types.UsageTerminalUnknown) != types.UsageTerminalFailure {
			t.Fatalf("unavailable provider terminal = %+v", observation)
		}
	}
}

func TestGrokUsageCapture_DoesNotInventHookIdentity(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGrokUsageCaptureUsecase(repository)
	result, err := capture.CaptureHookUnavailable(
		context.Background(),
		usecase.GrokUsageCaptureInput{SessionID: "session-1"},
	)
	if err != nil || result != (usecase.GrokUsageCaptureResult{}) || len(repository.observations) != 0 {
		t.Fatalf("CaptureHookUnavailable() = (%+v, %v), observations = %d", result, err, len(repository.observations))
	}
}

func TestGrokUsageCapture_RejectsInconsistentTerminalBoundary(t *testing.T) {
	repository := newGoogleUsageRepositoryStub()
	capture := usecase.NewGrokUsageCaptureUsecase(repository)
	input := usecase.GrokUsageCaptureInput{SessionID: "session-1", DeliveryID: "session_run"}
	for name, loaded := range map[string]application.GrokUsageLoadResult{
		"sample without boundary": {Samples: []application.GrokUsageSample{grokUsageSampleFixture()}},
		"boundary without sample": {BoundaryObserved: true},
		"multiple samples": {
			BoundaryObserved: true,
			Samples:          []application.GrokUsageSample{grokUsageSampleFixture(), grokUsageSampleFixture()},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := capture.CaptureHeadless(context.Background(), input, loaded); err == nil {
				t.Fatal("CaptureHeadless() error = nil")
			}
		})
	}
}

func grokUsageSampleFixture() application.GrokUsageSample {
	input, cached, output, reasoning, total := int64(21440), int64(0), int64(25), int64(20), int64(21465)
	return application.GrokUsageSample{
		RecordID: "headless_stream:request-1:session-1", SourceName: "headless_stream",
		SourceVersion: "0.2.106", Model: "grok-4.5-build",
		ObservedAt: time.Unix(0, 0).UTC(), TerminalCode: types.UsageTerminalSuccess, Available: true,
		Counters: application.GrokUsageCounters{
			InputTokens: &input, CachedInputTokens: &cached, OutputTokens: &output,
			ReasoningTokens: &reasoning, TotalTokens: &total,
		},
	}
}
