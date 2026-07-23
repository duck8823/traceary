package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

type claudeUsageSourceStub struct {
	result application.ClaudeUsageLoadResult
	err    error
}

func (s claudeUsageSourceStub) Load(
	context.Context,
	application.ClaudeUsageLoadCriteria,
) (application.ClaudeUsageLoadResult, error) {
	return s.result, s.err
}

func TestClaudeUsageCaptureUsecase_RecordsKnownZeroAndMissingFieldsOnce(t *testing.T) {
	t.Parallel()
	zero, input, cached, output := int64(0), int64(120), int64(80), int64(7)
	observedAt := time.Date(2026, 7, 23, 2, 3, 4, 0, time.UTC)
	source := claudeUsageSourceStub{result: application.ClaudeUsageLoadResult{
		Mode:             application.ClaudeUsageModeTranscriptCalls,
		BoundaryObserved: true,
		Samples: []application.ClaudeUsageSample{{
			RecordID: "transcript_calls:call-1", SourceName: "transcript_calls", SourceVersion: "schema-v1",
			Model: "claude-opus-4-1", Scope: types.UsageScopeCall, ObservedAt: observedAt,
			TerminalCode: types.UsageTerminalSuccess, Available: true,
			Counters: application.ClaudeUsageCounters{
				InputTokens: &input, CachedInputTokens: &cached, CacheWriteInputTokens: &zero,
				OutputTokens: &output,
			},
		}},
	}}
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewClaudeUsageCaptureUsecase(source, repository)
	captureInput := usecase.ClaudeUsageCaptureInput{SessionID: "session-1", DeliveryID: "event_id:stop-1"}

	first, err := sut.Capture(context.Background(), captureInput)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	second, err := sut.Capture(context.Background(), captureInput)
	if err != nil {
		t.Fatalf("Capture() replay error = %v", err)
	}
	if first.Applied != 1 || second.AlreadyApplied != 1 || len(repository.observations) != 1 {
		t.Fatalf("first=%+v second=%+v observations=%d", first, second, len(repository.observations))
	}
	for _, observation := range repository.observations {
		descriptor := observation.Descriptor()
		if descriptor.Scope() != types.UsageScopeCall ||
			descriptor.Accounting() != types.UsageAccountingAdditive ||
			descriptor.Source().Provider() != "anthropic" ||
			descriptor.Source().Model() != "claude-opus-4-1" {
			t.Fatalf("descriptor = %+v", descriptor)
		}
		counters := observation.Counters()
		if value, known := counters.CacheWriteInput().Value(); !known || value != 0 {
			t.Fatalf("cache creation = (%d, %t), want known zero", value, known)
		}
		if counters.ReasoningOutput().State() != types.UsageValueUnavailable ||
			counters.Total().State() != types.UsageValueUnavailable {
			t.Fatalf("unreported counters were not unavailable: %+v", counters)
		}
	}
}

func TestClaudeUsageCaptureUsecase_LegacyTerminalSummaryExcludesCallEvidence(t *testing.T) {
	t.Parallel()
	input, output := int64(25), int64(5)
	observedAt := time.Date(2026, 7, 23, 2, 3, 4, 0, time.UTC)
	source := claudeUsageSourceStub{result: application.ClaudeUsageLoadResult{
		Mode:             application.ClaudeUsageModeOneShotStream,
		BoundaryObserved: true,
		Samples: []application.ClaudeUsageSample{
			{
				RecordID: "one_shot_stream:run-1", SourceName: "one_shot_stream", SourceVersion: "schema-v1",
				Scope: types.UsageScopeRun, ObservedAt: observedAt, TerminalCode: types.UsageTerminalSuccess,
				Available: true, Counters: application.ClaudeUsageCounters{InputTokens: &input, OutputTokens: &output},
			},
			{
				RecordID: "transcript_calls:call-1", SourceName: "transcript_calls", SourceVersion: "schema-v1",
				Scope: types.UsageScopeCall, ObservedAt: observedAt, TerminalCode: types.UsageTerminalSuccess,
				Available: true, Counters: application.ClaudeUsageCounters{InputTokens: &input, OutputTokens: &output},
			},
		},
	}}
	repository := &codexUsageRepositoryFake{}
	_, err := usecase.NewClaudeUsageCaptureUsecase(source, repository).Capture(
		context.Background(), usecase.ClaudeUsageCaptureInput{SessionID: "session-1"},
	)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	additive, excluded := 0, 0
	for _, observation := range repository.observations {
		switch observation.Descriptor().Accounting() {
		case types.UsageAccountingAdditive:
			additive++
			if observation.Descriptor().Scope() != types.UsageScopeRun {
				t.Fatalf("additive scope = %q, want run", observation.Descriptor().Scope())
			}
		case types.UsageAccountingExcluded:
			excluded++
		}
	}
	if additive != 1 || excluded != 1 {
		t.Fatalf("additive=%d excluded=%d", additive, excluded)
	}
}

func TestClaudeUsageCaptureUsecase_RecordsStableUnavailableBoundary(t *testing.T) {
	t.Parallel()
	repository := &codexUsageRepositoryFake{}
	sut := usecase.NewClaudeUsageCaptureUsecase(
		claudeUsageSourceStub{result: application.ClaudeUsageLoadResult{
			Mode: application.ClaudeUsageModeTranscriptCalls,
		}},
		repository,
	)
	input := usecase.ClaudeUsageCaptureInput{
		SessionID: "session-1", DeliveryID: "event_id:stop-missing",
	}
	first, err := sut.Capture(context.Background(), input)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	second, err := sut.Capture(context.Background(), input)
	if err != nil {
		t.Fatalf("Capture() replay error = %v", err)
	}
	if first.Applied != 1 || first.Unavailable != 1 || second.AlreadyApplied != 1 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	for _, observation := range repository.observations {
		if observation.Descriptor().Accounting() != types.UsageAccountingExcluded ||
			observation.Counters().Availability() != types.UsageAvailabilityUnavailable {
			t.Fatalf("unavailable observation = %+v", observation)
		}
	}
}
