package usecase

import (
	"context"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

const (
	reportTopCommandLimit      = 15
	reportFailureLoopLimit     = 10
	reportFailureSampleLimit   = 8
	reportLoopSampleLimit      = 3
	reportFailureLoopThreshold = 3
)

// ReportCommandUsecase aggregates structured command outcomes for reports.
type ReportCommandUsecase interface {
	Summarize(ctx context.Context, criteria apptypes.EventListCriteria) (apptypes.ReportCommandSummary, error)
}

type reportCommandUsecase struct {
	query queryservice.CommandAuditQueryService
}

// NewReportCommandUsecase creates the structured command report use case.
func NewReportCommandUsecase(query queryservice.CommandAuditQueryService) ReportCommandUsecase {
	return &reportCommandUsecase{query: query}
}

func (u *reportCommandUsecase) Summarize(ctx context.Context, criteria apptypes.EventListCriteria) (apptypes.ReportCommandSummary, error) {
	if u.query == nil {
		return apptypes.ReportCommandSummary{}, xerrors.New("command audit query service is not configured")
	}
	if criteria.Limit() <= 0 {
		return apptypes.ReportCommandSummary{}, xerrors.New("limit must be greater than or equal to 1")
	}
	if criteria.Offset() != 0 {
		return apptypes.ReportCommandSummary{}, xerrors.New("offset must be zero for report command aggregation")
	}
	records, err := u.query.ListReportWindow(ctx, criteria)
	if err != nil {
		return apptypes.ReportCommandSummary{}, xerrors.Errorf("failed to list report command audits: %w", err)
	}
	return summarizeReportCommands(records), nil
}

func summarizeReportCommands(records []apptypes.ReportCommandRecord) apptypes.ReportCommandSummary {
	commandAgg := map[string]*apptypes.ReportCommandRow{}
	type failureLoopKey struct{ command, workspace, agent string }
	failureLoops := map[failureLoopKey]*apptypes.ReportFailureLoop{}
	summary := apptypes.ReportCommandSummary{
		FailuresByClient: map[string]int{},
		FailuresByReason: map[string]int{},
		FailureSamples:   make([]string, 0, reportFailureSampleLimit),
	}
	for _, record := range records {
		command := strings.TrimSpace(record.CommandName.String())
		if command == "" {
			command = "unknown"
		}
		row := commandAgg[command]
		if row == nil {
			row = &apptypes.ReportCommandRow{Command: command, SampleEventID: record.EventID.String()}
			commandAgg[command] = row
		}
		row.Count++
		if !record.IsFailure() {
			continue
		}
		row.FailedCount++
		summary.FailureTotal++
		client := record.Client.String()
		if client == "" {
			client = "(empty)"
		}
		summary.FailuresByClient[client]++
		summary.FailuresByReason[record.EffectiveFailureReason().String()]++
		if len(summary.FailureSamples) < reportFailureSampleLimit {
			summary.FailureSamples = append(summary.FailureSamples, record.EventID.String())
		}
		key := failureLoopKey{command: command, workspace: record.Workspace.String(), agent: record.Agent.String()}
		loop := failureLoops[key]
		if loop == nil {
			loop = &apptypes.ReportFailureLoop{Command: command, Workspace: key.workspace, Agent: key.agent}
			failureLoops[key] = loop
		}
		loop.Count++
		if len(loop.SampleEventIDs) < reportLoopSampleLimit {
			loop.SampleEventIDs = append(loop.SampleEventIDs, record.EventID.String())
		}
	}

	for _, row := range commandAgg {
		if row.Count > 0 {
			row.FailureRate = float64(row.FailedCount) / float64(row.Count)
		}
		summary.TopCommands = append(summary.TopCommands, *row)
	}
	sort.Slice(summary.TopCommands, func(i, j int) bool {
		if summary.TopCommands[i].Count == summary.TopCommands[j].Count {
			return summary.TopCommands[i].Command < summary.TopCommands[j].Command
		}
		return summary.TopCommands[i].Count > summary.TopCommands[j].Count
	})
	if len(summary.TopCommands) > reportTopCommandLimit {
		summary.TopCommands = summary.TopCommands[:reportTopCommandLimit]
	}

	for _, loop := range failureLoops {
		if loop.Count >= reportFailureLoopThreshold {
			summary.FailureLoops = append(summary.FailureLoops, *loop)
		}
	}
	sort.Slice(summary.FailureLoops, func(i, j int) bool {
		if summary.FailureLoops[i].Count == summary.FailureLoops[j].Count {
			return summary.FailureLoops[i].Command < summary.FailureLoops[j].Command
		}
		return summary.FailureLoops[i].Count > summary.FailureLoops[j].Count
	})
	if len(summary.FailureLoops) > reportFailureLoopLimit {
		summary.FailureLoops = summary.FailureLoops[:reportFailureLoopLimit]
	}
	return summary
}
