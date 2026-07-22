package usecase_test

import (
	"context"
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
	return apptypes.ReportWindow{
		Sessions: sessions, Events: events, Commands: commands,
		Extents: apptypes.ReportSourceExtents{Sessions: sessionsExtent, Events: eventsExtent, Commands: commandsExtent},
	}
}
