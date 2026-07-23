package usecase_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

type reportQueryStub struct {
	window   apptypes.ReportWindow
	criteria apptypes.ReportCriteria
}

func (s *reportQueryStub) LoadReportWindow(_ context.Context, criteria apptypes.ReportCriteria) (apptypes.ReportWindow, error) {
	s.criteria = criteria
	return s.window, nil
}

func TestReportUsecaseGenerate_CompleteWindowIncludesRates(t *testing.T) {
	t.Parallel()
	criteria := reportCriteria(t, 0)
	event := reportEventMetadata(t, "event-prompt", types.EventKindPrompt, "session-1", time.Date(2026, 7, 21, 2, 0, 0, 0, time.UTC))
	transcript := reportEventMetadata(t, "event-transcript", types.EventKindTranscript, "session-1", time.Date(2026, 7, 21, 3, 0, 0, 0, time.UTC))
	command := reportCommandRecord("event-command", "go", types.CommandFailureReasonExitCode, types.Some(1), true)
	window := reportWindow(t, criteria, []apptypes.ReportSessionRecord{{Client: "codex", StartedAt: time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC), TotalEvents: 3, CommandCount: 1}}, []apptypes.EventMetadata{event, transcript}, []apptypes.ReportCommandRecord{command}, false)
	stub := &reportQueryStub{window: window}

	got, err := usecase.NewReportUsecase(stub).Generate(context.Background(), criteria)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got.Aggregation.Coverage != apptypes.ReportCoverageComplete {
		t.Fatalf("aggregation coverage = %q", got.Aggregation.Coverage)
	}
	if len(got.CaptureCoverage) != 1 || got.CaptureCoverage[0].PromptTranscriptMissingRatio == nil || *got.CaptureCoverage[0].PromptTranscriptMissingRatio != 0 {
		t.Fatalf("capture coverage = %+v", got.CaptureCoverage)
	}
	if len(got.TopCommands) != 1 || got.TopCommands[0].FailureRate == nil || *got.TopCommands[0].FailureRate != 1 {
		t.Fatalf("top commands = %+v", got.TopCommands)
	}
	if stub.criteria.PageSize() != criteria.PageSize() || stub.criteria.ResultCap() != 0 {
		t.Fatalf("forwarded criteria page=%d cap=%d", stub.criteria.PageSize(), stub.criteria.ResultCap())
	}
}

func TestReportUsecaseGenerate_PartialWindowOmitsRates(t *testing.T) {
	t.Parallel()
	criteria := reportCriteria(t, 1)
	event := reportEventMetadata(t, "event-prompt", types.EventKindPrompt, "session-1", time.Date(2026, 7, 21, 2, 0, 0, 0, time.UTC))
	command := reportCommandRecord("event-command", "go", types.CommandFailureReasonExitCode, types.Some(1), true)
	window := reportWindow(t, criteria, []apptypes.ReportSessionRecord{{Client: "codex", StartedAt: time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC)}}, []apptypes.EventMetadata{event}, []apptypes.ReportCommandRecord{command}, true)

	got, err := usecase.NewReportUsecase(&reportQueryStub{window: window}).Generate(context.Background(), criteria)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got.Aggregation.Coverage != apptypes.ReportCoveragePartial {
		t.Fatalf("aggregation coverage = %q", got.Aggregation.Coverage)
	}
	if len(got.CaptureCoverage) != 1 || got.CaptureCoverage[0].PromptTranscriptMissingRatio != nil {
		t.Fatalf("partial capture ratio must be absent: %+v", got.CaptureCoverage)
	}
	if len(got.TopCommands) != 1 || got.TopCommands[0].FailureRate != nil {
		t.Fatalf("partial failure rate must be absent: %+v", got.TopCommands)
	}
	if !got.Aggregation.Sources.Events.ResponseTruncated || got.Aggregation.Sources.Events.TruncationReason != "result_cap" {
		t.Fatalf("event extent = %+v", got.Aggregation.Sources.Events)
	}
}

func TestReportUsecaseGenerate_AggregatesUsageWithoutExcludedOrRunDuplicates(t *testing.T) {
	t.Parallel()
	criteria := reportCriteria(t, 0)
	knownZero, err := types.KnownUsageValue(0)
	if err != nil {
		t.Fatal(err)
	}
	knownTen, err := types.KnownUsageValue(10)
	if err != nil {
		t.Fatal(err)
	}
	knownFive, err := types.KnownUsageValue(5)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := types.UnavailableUsageValue()
	countersA, err := types.UsageCountersOf(
		knownTen, unavailable, unavailable, knownZero, unavailable, unavailable,
	)
	if err != nil {
		t.Fatal(err)
	}
	countersB, err := types.UsageCountersOf(
		knownFive, unavailable, unavailable, knownFive, unavailable, unavailable,
	)
	if err != nil {
		t.Fatal(err)
	}
	excludedCounters, err := types.UsageCountersOf(
		knownTen, knownTen, knownTen, knownTen, knownTen, knownTen,
	)
	if err != nil {
		t.Fatal(err)
	}
	providerCost, err := types.ProviderReportedUsageCost(100, "USD")
	if err != nil {
		t.Fatal(err)
	}
	estimatedCost, err := types.EstimatedUsageCost(50, "USD", "prices-v1")
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 7, 21, 2, 0, 0, 0, time.UTC)
	base := apptypes.ReportUsageRecord{
		ObservedAt: observedAt, Engine: "codex", Provider: "openai", Model: "gpt-5.6",
		Accounting: types.UsageAccountingAdditive, TerminalCode: types.UsageTerminalSuccess,
		RunHost: "codex", RunID: "run-1", Repository: "github.com/duck8823/traceary",
		TicketRef: "GH#1449", PullRequest: types.Some(int64(1501)), BatchID: "batch-1",
		PacketBytes: types.Some(int64(100)), ToolOutputBytes: types.Some(int64(50)),
	}
	first := base
	first.ObservationID, first.Counters, first.Cost = "usage-1", countersA, providerCost
	second := base
	second.ObservationID, second.Counters, second.Cost = "usage-2", countersB, estimatedCost
	excluded := base
	excluded.ObservationID = "usage-excluded"
	excluded.Accounting = types.UsageAccountingExcluded
	excluded.Counters = excludedCounters
	excluded.Cost = providerCost
	excluded.PacketBytes = types.Some(int64(999))
	excluded.ToolOutputBytes = types.Some(int64(999))

	window := reportWindow(t, criteria, nil, nil, nil, false)
	window.Usage = []apptypes.ReportUsageRecord{excluded, first, second}
	usageExtent, err := apptypes.ReportSourceExtentOf(
		[]time.Time{observedAt, observedAt, observedAt}, criteria.PageSize(), criteria.ResultCap(), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	window.Extents.Usage = usageExtent

	got, err := usecase.NewReportUsecase(&reportQueryStub{window: window}).Generate(context.Background(), criteria)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got.UsageScanCount != 3 || len(got.Usage.Aggregates) != 1 || len(got.Usage.Runs) != 1 {
		t.Fatalf("usage snapshot = %+v", got.Usage)
	}
	aggregate := got.Usage.Aggregates[0]
	if aggregate.Observations != 3 || aggregate.Accounted != 2 || aggregate.Excluded != 1 {
		t.Fatalf("usage counts = %+v", aggregate)
	}
	if aggregate.InputTokens.KnownObservations != 2 ||
		aggregate.InputTokens.UnavailableObservations != 0 ||
		aggregate.InputTokens.Sum != 15 {
		t.Fatalf("input tokens = %+v", aggregate.InputTokens)
	}
	if aggregate.OutputTokens.KnownObservations != 2 || aggregate.OutputTokens.Sum != 5 {
		t.Fatalf("output tokens = %+v", aggregate.OutputTokens)
	}
	if len(aggregate.Costs) != 2 ||
		aggregate.Costs[0].Origin != "estimated" ||
		aggregate.Costs[1].Origin != "provider_reported" {
		t.Fatalf("costs = %+v", aggregate.Costs)
	}
	run := got.Usage.Runs[0]
	if run.Runs != 1 || run.PacketBytes.KnownRuns != 1 || run.PacketBytes.Sum != 100 ||
		run.ToolOutputBytes.KnownRuns != 1 || run.ToolOutputBytes.Sum != 50 ||
		run.WallTimeMS.UnavailableRuns != 1 {
		t.Fatalf("run aggregate = %+v", run)
	}
	if aggregate.RoleAvailability != "unavailable" || aggregate.RoundAvailability != "unavailable" ||
		run.RoleAvailability != "unavailable" || run.RoundAvailability != "unavailable" {
		t.Fatalf("unrecorded dimensions were not explicit: aggregate=%+v run=%+v", aggregate, run)
	}
}

func TestReportUsecaseGenerate_RejectsUsageSumOverflow(t *testing.T) {
	t.Parallel()
	criteria := reportCriteria(t, 0)
	observedAt := time.Date(2026, 7, 21, 2, 0, 0, 0, time.UTC)
	knownMax, err := types.KnownUsageValue(math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	knownOne, err := types.KnownUsageValue(1)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := types.UnavailableUsageValue()
	maxCounters, err := types.UsageCountersOf(
		knownMax, unavailable, unavailable, unavailable, unavailable, unavailable,
	)
	if err != nil {
		t.Fatal(err)
	}
	oneCounters, err := types.UsageCountersOf(
		knownOne, unavailable, unavailable, unavailable, unavailable, unavailable,
	)
	if err != nil {
		t.Fatal(err)
	}
	unavailableCounters, err := types.UsageCountersOf(
		unavailable, unavailable, unavailable, unavailable, unavailable, unavailable,
	)
	if err != nil {
		t.Fatal(err)
	}
	maxCost, err := types.ProviderReportedUsageCost(math.MaxInt64, "USD")
	if err != nil {
		t.Fatal(err)
	}
	oneCost, err := types.ProviderReportedUsageCost(1, "USD")
	if err != nil {
		t.Fatal(err)
	}

	base := apptypes.ReportUsageRecord{
		ObservedAt: observedAt, Engine: "codex", Provider: "openai", Model: "gpt-5.6",
		Accounting: types.UsageAccountingAdditive, TerminalCode: types.UsageTerminalSuccess,
		Counters: unavailableCounters, Cost: types.UnavailableUsageCost(),
	}
	tests := []struct {
		name    string
		records []apptypes.ReportUsageRecord
	}{
		{
			name: "token total",
			records: []apptypes.ReportUsageRecord{
				func() apptypes.ReportUsageRecord {
					row := base
					row.ObservationID, row.Counters = "usage-max", maxCounters
					return row
				}(),
				func() apptypes.ReportUsageRecord {
					row := base
					row.ObservationID, row.Counters = "usage-one", oneCounters
					return row
				}(),
			},
		},
		{
			name: "cost total",
			records: []apptypes.ReportUsageRecord{
				func() apptypes.ReportUsageRecord {
					row := base
					row.ObservationID, row.Cost = "cost-max", maxCost
					return row
				}(),
				func() apptypes.ReportUsageRecord {
					row := base
					row.ObservationID, row.Cost = "cost-one", oneCost
					return row
				}(),
			},
		},
		{
			name: "run packet bytes",
			records: []apptypes.ReportUsageRecord{
				func() apptypes.ReportUsageRecord {
					row := base
					row.ObservationID, row.RunHost, row.RunID = "run-max", "codex", "run-max"
					row.PacketBytes = types.Some(int64(math.MaxInt64))
					return row
				}(),
				func() apptypes.ReportUsageRecord {
					row := base
					row.ObservationID, row.RunHost, row.RunID = "run-one", "codex", "run-one"
					row.PacketBytes = types.Some(int64(1))
					return row
				}(),
			},
		},
		{
			name: "run tool output bytes",
			records: []apptypes.ReportUsageRecord{
				func() apptypes.ReportUsageRecord {
					row := base
					row.ObservationID, row.RunHost, row.RunID = "tool-max", "codex", "tool-max"
					row.ToolOutputBytes = types.Some(int64(math.MaxInt64))
					return row
				}(),
				func() apptypes.ReportUsageRecord {
					row := base
					row.ObservationID, row.RunHost, row.RunID = "tool-one", "codex", "tool-one"
					row.ToolOutputBytes = types.Some(int64(1))
					return row
				}(),
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			window := reportWindow(t, criteria, nil, nil, nil, false)
			window.Usage = test.records
			times := make([]time.Time, len(test.records))
			for index := range times {
				times[index] = observedAt
			}
			usageExtent, extentErr := apptypes.ReportSourceExtentOf(
				times, criteria.PageSize(), criteria.ResultCap(), false,
			)
			if extentErr != nil {
				t.Fatal(extentErr)
			}
			window.Extents.Usage = usageExtent
			_, generateErr := usecase.NewReportUsecase(&reportQueryStub{window: window}).
				Generate(context.Background(), criteria)
			if generateErr == nil || !strings.Contains(generateErr.Error(), "overflows int64") {
				t.Fatalf("Generate() error = %v, want int64 overflow", generateErr)
			}
		})
	}
}

func reportCriteria(t *testing.T, resultCap int) apptypes.ReportCriteria {
	t.Helper()
	criteria, err := apptypes.ReportCriteriaFrom(
		"2026-07-20", "2026-07-22", "UTC",
		time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
		"workspace", "codex", 2, resultCap,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom() error = %v", err)
	}
	return criteria
}

func reportEventMetadata(t *testing.T, id string, kind types.EventKind, sessionID string, createdAt time.Time) apptypes.EventMetadata {
	t.Helper()
	extent, err := apptypes.EventBodyExtentOf(types.Some(1024), 1024, types.Some(false), types.Some(false), types.Some(1))
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}
	metadata, err := apptypes.EventMetadataOf(
		types.EventID(id), kind, "codex", "codex", types.SessionID(sessionID), "workspace", "hook",
		createdAt, extent, types.None[apptypes.CommandAuditMetadata](),
	)
	if err != nil {
		t.Fatalf("EventMetadataOf() error = %v", err)
	}
	return metadata
}

func reportWindow(
	t *testing.T,
	criteria apptypes.ReportCriteria,
	sessions []apptypes.ReportSessionRecord,
	events []apptypes.EventMetadata,
	commands []apptypes.ReportCommandRecord,
	partial bool,
) apptypes.ReportWindow {
	t.Helper()
	sessionTimes := make([]time.Time, 0, len(sessions))
	for _, record := range sessions {
		sessionTimes = append(sessionTimes, record.StartedAt)
	}
	eventTimes := make([]time.Time, 0, len(events))
	for _, record := range events {
		eventTimes = append(eventTimes, record.CreatedAt())
	}
	commandTimes := make([]time.Time, 0, len(commands))
	for _, record := range commands {
		commandTimes = append(commandTimes, record.CreatedAt)
	}
	sessionsExtent, err := apptypes.ReportSourceExtentOf(sessionTimes, criteria.PageSize(), criteria.ResultCap(), partial)
	if err != nil {
		t.Fatalf("session extent error = %v", err)
	}
	eventsExtent, err := apptypes.ReportSourceExtentOf(eventTimes, criteria.PageSize(), criteria.ResultCap(), partial)
	if err != nil {
		t.Fatalf("event extent error = %v", err)
	}
	commandsExtent, err := apptypes.ReportSourceExtentOf(commandTimes, criteria.PageSize(), criteria.ResultCap(), partial)
	if err != nil {
		t.Fatalf("command extent error = %v", err)
	}
	usageExtent, err := apptypes.ReportSourceExtentOf(nil, criteria.PageSize(), criteria.ResultCap(), false)
	if err != nil {
		t.Fatalf("usage extent error = %v", err)
	}
	return apptypes.ReportWindow{
		Sessions: sessions, Events: events, Commands: commands,
		Extents: apptypes.ReportSourceExtents{
			Sessions: sessionsExtent, Events: eventsExtent, Commands: commandsExtent, Usage: usageExtent,
		},
	}
}
