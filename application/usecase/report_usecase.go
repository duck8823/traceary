package usecase

import (
	"context"
	"sort"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

// ReportUsecase generates the shared CLI/MCP report snapshot.
type ReportUsecase interface {
	Generate(ctx context.Context, criteria apptypes.ReportCriteria) (apptypes.ReportSnapshot, error)
}

type reportUsecase struct {
	query queryservice.ReportQueryService
}

// NewReportUsecase creates the shared CLI/MCP aggregate generator.
func NewReportUsecase(query queryservice.ReportQueryService) ReportUsecase {
	return &reportUsecase{query: query}
}

func (u *reportUsecase) Generate(ctx context.Context, criteria apptypes.ReportCriteria) (apptypes.ReportSnapshot, error) {
	if u.query == nil {
		return apptypes.ReportSnapshot{}, xerrors.New("report query service is not configured")
	}
	window, err := u.query.LoadReportWindow(ctx, criteria)
	if err != nil {
		return apptypes.ReportSnapshot{}, xerrors.Errorf("failed to load report window: %w", err)
	}
	if err := validateReportWindow(window); err != nil {
		return apptypes.ReportSnapshot{}, err
	}
	return buildReportSnapshot(criteria, window), nil
}

func validateReportWindow(window apptypes.ReportWindow) error {
	tests := []struct {
		name   string
		count  int
		extent apptypes.ReportSourceExtent
	}{
		{name: "sessions", count: len(window.Sessions), extent: window.Extents.Sessions},
		{name: "events", count: len(window.Events), extent: window.Extents.Events},
		{name: "commands", count: len(window.Commands), extent: window.Extents.Commands},
	}
	for _, test := range tests {
		if test.extent.ObservedCount != test.count {
			return xerrors.Errorf("report %s extent count %d does not match %d rows", test.name, test.extent.ObservedCount, test.count)
		}
		switch test.extent.Coverage {
		case apptypes.ReportCoverageComplete:
			if test.extent.ResponseTruncated {
				return xerrors.Errorf("complete report %s extent cannot be truncated", test.name)
			}
		case apptypes.ReportCoveragePartial:
			if !test.extent.ResponseTruncated || test.extent.TruncationReason != "result_cap" {
				return xerrors.Errorf("partial report %s extent lacks result-cap provenance", test.name)
			}
		default:
			return xerrors.Errorf("report %s extent has unknown coverage %q", test.name, test.extent.Coverage)
		}
	}
	return nil
}

func buildReportSnapshot(criteria apptypes.ReportCriteria, window apptypes.ReportWindow) apptypes.ReportSnapshot {
	interval := criteria.Interval()
	sessions := summarizeReportSessions(window.Sessions)
	coverage := summarizeReportCoverage(window.Events, window.Extents.Events.Coverage == apptypes.ReportCoverageComplete)
	commandSummary := summarizeReportCommands(window.Commands)
	commands := reportCommandOutputs(commandSummary, window.Extents.Commands.Coverage == apptypes.ReportCoverageComplete)
	loops := make([]apptypes.ReportFailureLoopOutput, 0, len(commandSummary.FailureLoops))
	for _, loop := range commandSummary.FailureLoops {
		loops = append(loops, apptypes.ReportFailureLoopOutput(loop))
	}
	aggregationCoverage := apptypes.ReportCoverageComplete
	if window.Extents.Sessions.Coverage == apptypes.ReportCoveragePartial ||
		window.Extents.Events.Coverage == apptypes.ReportCoveragePartial ||
		window.Extents.Commands.Coverage == apptypes.ReportCoveragePartial {
		aggregationCoverage = apptypes.ReportCoveragePartial
	}
	return apptypes.ReportSnapshot{
		Period: apptypes.ReportPeriod{
			From:                   formatReportCompatibilityTime(interval.EffectiveFromInclusive()),
			To:                     formatReportCompatibilityTime(interval.EffectiveToExclusive()),
			RequestedFrom:          interval.RequestedFrom(),
			RequestedTo:            interval.RequestedTo(),
			EffectiveFromInclusive: formatReportTime(interval.EffectiveFromInclusive()),
			EffectiveToExclusive:   formatReportTime(interval.EffectiveToExclusive()),
			Timezone:               interval.Timezone(),
			SnapshotAt:             formatReportTime(interval.SnapshotAt()),
			FromDateOnly:           interval.FromIsDateOnly(),
			ToDateOnly:             interval.ToIsDateOnly(),
		},
		Aggregation: apptypes.ReportAggregation{
			Coverage: aggregationCoverage, PageSize: criteria.PageSize(),
			ResultCap: criteria.ResultCap(), Sources: window.Extents,
		},
		Workspace: criteria.Workspace().String(), ClientFilter: criteria.Client().String(),
		Sessions: sessions, CaptureCoverage: coverage,
		Failures: apptypes.ReportFailures{
			Total: commandSummary.FailureTotal, ByClient: commandSummary.FailuresByClient,
			ByReason: commandSummary.FailuresByReason, Samples: commandSummary.FailureSamples,
		},
		TopCommands: commands, FailureLoops: loops,
		EventScanCount: len(window.Events), SessionScanCount: len(window.Sessions),
	}
}

func summarizeReportSessions(records []apptypes.ReportSessionRecord) []apptypes.ReportSessionRow {
	byClient := map[string]*apptypes.ReportSessionRow{}
	for _, record := range records {
		client := record.Client.String()
		if client == "" {
			client = "(empty)"
		}
		row := byClient[client]
		if row == nil {
			row = &apptypes.ReportSessionRow{Client: client}
			byClient[client] = row
		}
		row.Sessions++
		row.TotalEvents += record.TotalEvents
		row.CommandCount += record.CommandCount
	}
	result := make([]apptypes.ReportSessionRow, 0, len(byClient))
	for _, row := range byClient {
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Sessions == result[j].Sessions {
			return result[i].Client < result[j].Client
		}
		return result[i].Sessions > result[j].Sessions
	})
	return result
}

func summarizeReportCoverage(events []apptypes.EventMetadata, complete bool) []apptypes.ReportCoverageRow {
	byClient := map[string][]EventCoverageInput{}
	for _, event := range events {
		client := event.Client().String()
		if client == "" {
			client = "(empty)"
		}
		byClient[client] = append(byClient[client], EventCoverageInput{
			SessionID: event.SessionID().String(), Kind: event.Kind(),
		})
	}
	result := make([]apptypes.ReportCoverageRow, 0, len(byClient))
	for client, inputs := range byClient {
		summary := SummarizeSessionEventCoverage(inputs)
		row := apptypes.ReportCoverageRow{
			Client: client, Sessions: summary.Sessions, WithPrompt: summary.WithPrompt,
			WithTranscript: summary.WithTranscript, WithCommand: summary.WithCommand,
			PromptTranscriptMissing: summary.PromptTranscriptMissing,
		}
		if complete {
			ratio := summary.PromptTranscriptMissingRatio()
			row.PromptTranscriptMissingRatio = &ratio
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Client < result[j].Client })
	return result
}

func reportCommandOutputs(summary apptypes.ReportCommandSummary, complete bool) []apptypes.ReportCommandOutput {
	result := make([]apptypes.ReportCommandOutput, 0, len(summary.TopCommands))
	for _, row := range summary.TopCommands {
		output := apptypes.ReportCommandOutput{
			Command: row.Command, Count: row.Count, FailedCount: row.FailedCount,
			SampleEventID: row.SampleEventID,
		}
		if complete {
			rate := row.FailureRate
			output.FailureRate = &rate
		}
		result = append(result, output)
	}
	return result
}

func formatReportTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatReportCompatibilityTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
