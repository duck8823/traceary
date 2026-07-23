package usecase

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
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
	return buildReportSnapshot(criteria, window)
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
		{name: "usage", count: len(window.Usage), extent: window.Extents.Usage},
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
	for _, record := range window.Usage {
		if strings.TrimSpace(record.ObservationID) == "" || record.ObservedAt.IsZero() {
			return xerrors.Errorf("report usage row has invalid identity or timestamp")
		}
		switch record.Accounting {
		case domtypes.UsageAccountingAdditive,
			domtypes.UsageAccountingLatestSnapshot,
			domtypes.UsageAccountingExcluded:
		default:
			return xerrors.Errorf("report usage row has unknown accounting %q", record.Accounting)
		}
		if record.Counters.Availability() == domtypes.UsageAvailabilityUnknown {
			return xerrors.Errorf("finalized report usage row has unknown counters")
		}
		if record.Cost.State() == domtypes.UsageCostUnknown {
			return xerrors.Errorf("finalized report usage row has unknown cost")
		}
		if record.RunID == "" {
			if record.RunHost != "" {
				return xerrors.Errorf("report usage row has incomplete run identity")
			}
			if _, present := record.PacketBytes.Value(); present {
				return xerrors.Errorf("report usage row has packet bytes without a run")
			}
			if _, present := record.ToolOutputBytes.Value(); present {
				return xerrors.Errorf("report usage row has tool bytes without a run")
			}
		} else if record.RunHost == "" {
			return xerrors.Errorf("report usage row has incomplete run identity")
		} else if record.Engine != record.RunHost {
			return xerrors.Errorf("report usage row engine does not match run host")
		}
	}
	return nil
}

func buildReportSnapshot(
	criteria apptypes.ReportCriteria,
	window apptypes.ReportWindow,
) (apptypes.ReportSnapshot, error) {
	interval := criteria.Interval()
	sessions := summarizeReportSessions(window.Sessions)
	coverage := summarizeReportCoverage(window.Events, window.Extents.Events.Coverage == apptypes.ReportCoverageComplete)
	commandSummary := summarizeReportCommands(window.Commands)
	commands := reportCommandOutputs(commandSummary, window.Extents.Commands.Coverage == apptypes.ReportCoverageComplete)
	usage, err := summarizeReportUsage(window.Usage)
	if err != nil {
		return apptypes.ReportSnapshot{}, xerrors.Errorf("failed to aggregate report usage: %w", err)
	}
	loops := make([]apptypes.ReportFailureLoopOutput, 0, len(commandSummary.FailureLoops))
	for _, loop := range commandSummary.FailureLoops {
		loops = append(loops, apptypes.ReportFailureLoopOutput(loop))
	}
	aggregationCoverage := apptypes.ReportCoverageComplete
	if window.Extents.Sessions.Coverage == apptypes.ReportCoveragePartial ||
		window.Extents.Events.Coverage == apptypes.ReportCoveragePartial ||
		window.Extents.Commands.Coverage == apptypes.ReportCoveragePartial ||
		window.Extents.Usage.Coverage == apptypes.ReportCoveragePartial {
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
		TopCommands: commands, FailureLoops: loops, Usage: usage,
		EventScanCount: len(window.Events), SessionScanCount: len(window.Sessions),
		UsageScanCount: len(window.Usage),
	}, nil
}

type reportUsageKey struct {
	provider, engine, model, repository, ticket, batch string
	pullRequest                                        int64
	hasPullRequest                                     bool
}

type reportUsageCostKey struct {
	origin, currency, priceTableVersion string
}

type reportUsageAccumulator struct {
	row   apptypes.ReportUsageAggregateRow
	costs map[reportUsageCostKey]*apptypes.ReportUsageCostRow
}

type reportUsageRunKey struct {
	engine, repository, ticket, batch string
	pullRequest                       int64
	hasPullRequest                    bool
}

type reportUsageRunAccumulator struct {
	row apptypes.ReportUsageRunAggregateRow
}

func summarizeReportUsage(records []apptypes.ReportUsageRecord) (apptypes.ReportUsageSnapshot, error) {
	usageByKey := map[reportUsageKey]*reportUsageAccumulator{}
	runsByKey := map[reportUsageRunKey]*reportUsageRunAccumulator{}
	seenRuns := map[string]struct{}{}

	for _, record := range records {
		key := newReportUsageKey(record)
		accumulator := usageByKey[key]
		if accumulator == nil {
			accumulator = &reportUsageAccumulator{
				row: apptypes.ReportUsageAggregateRow{
					Provider: key.provider, Engine: key.engine, Model: key.model,
					RoleAvailability: "unavailable",
					Repository:       key.repository, TicketRef: key.ticket, BatchID: key.batch,
					RoundAvailability: "unavailable",
					TerminalCodes:     map[string]int{},
				},
				costs: map[reportUsageCostKey]*apptypes.ReportUsageCostRow{},
			}
			if key.hasPullRequest {
				value := key.pullRequest
				accumulator.row.PullRequest = &value
			}
			usageByKey[key] = accumulator
		}
		if err := accumulateReportUsageObservation(accumulator, record); err != nil {
			return apptypes.ReportUsageSnapshot{}, xerrors.Errorf(
				"failed to aggregate usage observation %q: %w", record.ObservationID, err,
			)
		}

		if record.RunID == "" || record.Accounting == domtypes.UsageAccountingExcluded {
			continue
		}
		runIdentity := record.RunHost + "\x00" + record.RunID
		if _, present := seenRuns[runIdentity]; present {
			continue
		}
		seenRuns[runIdentity] = struct{}{}
		runKey := newReportUsageRunKey(record)
		runAccumulator := runsByKey[runKey]
		if runAccumulator == nil {
			runAccumulator = &reportUsageRunAccumulator{row: apptypes.ReportUsageRunAggregateRow{
				Engine: runKey.engine, RoleAvailability: "unavailable",
				Repository: key.repository, TicketRef: key.ticket, BatchID: key.batch,
				RoundAvailability: "unavailable",
			}}
			if runKey.hasPullRequest {
				value := runKey.pullRequest
				runAccumulator.row.PullRequest = &value
			}
			runsByKey[runKey] = runAccumulator
		}
		if err := accumulateReportUsageRun(runAccumulator, record); err != nil {
			return apptypes.ReportUsageSnapshot{}, xerrors.Errorf(
				"failed to aggregate usage run %q/%q: %w", record.RunHost, record.RunID, err,
			)
		}
	}

	aggregates := make([]apptypes.ReportUsageAggregateRow, 0, len(usageByKey))
	for _, accumulator := range usageByKey {
		accumulator.row.Costs = sortedReportUsageCosts(accumulator.costs)
		aggregates = append(aggregates, accumulator.row)
	}
	sort.Slice(aggregates, func(i, j int) bool {
		return reportUsageAggregateOrder(aggregates[i]) < reportUsageAggregateOrder(aggregates[j])
	})
	runs := make([]apptypes.ReportUsageRunAggregateRow, 0, len(runsByKey))
	for _, accumulator := range runsByKey {
		runs = append(runs, accumulator.row)
	}
	sort.Slice(runs, func(i, j int) bool {
		return reportUsageRunOrder(runs[i]) < reportUsageRunOrder(runs[j])
	})
	return apptypes.ReportUsageSnapshot{Aggregates: aggregates, Runs: runs}, nil
}

func newReportUsageKey(record apptypes.ReportUsageRecord) reportUsageKey {
	key := reportUsageKey{
		provider: record.Provider, engine: record.Engine, model: record.Model,
		repository: record.Repository, ticket: record.TicketRef, batch: record.BatchID,
	}
	if value, present := record.PullRequest.Value(); present {
		key.pullRequest, key.hasPullRequest = value, true
	}
	return key
}

func newReportUsageRunKey(record apptypes.ReportUsageRecord) reportUsageRunKey {
	key := reportUsageRunKey{
		engine: record.Engine, repository: record.Repository,
		ticket: record.TicketRef, batch: record.BatchID,
	}
	if value, present := record.PullRequest.Value(); present {
		key.pullRequest, key.hasPullRequest = value, true
	}
	return key
}

func accumulateReportUsageObservation(
	accumulator *reportUsageAccumulator,
	record apptypes.ReportUsageRecord,
) error {
	row := &accumulator.row
	row.Observations++
	row.TerminalCodes[record.TerminalCode.String()]++
	if record.Accounting == domtypes.UsageAccountingExcluded {
		row.Excluded++
		return nil
	}
	row.Accounted++
	metrics := []struct {
		name   string
		target *apptypes.ReportUsageMetric
		value  domtypes.UsageValue
	}{
		{name: "input tokens", target: &row.InputTokens, value: record.Counters.Input()},
		{name: "cached input tokens", target: &row.CachedInputTokens, value: record.Counters.CachedInput()},
		{name: "cache-write input tokens", target: &row.CacheWriteTokens, value: record.Counters.CacheWriteInput()},
		{name: "output tokens", target: &row.OutputTokens, value: record.Counters.Output()},
		{name: "reasoning output tokens", target: &row.ReasoningTokens, value: record.Counters.ReasoningOutput()},
		{name: "total tokens", target: &row.TotalTokens, value: record.Counters.Total()},
	}
	for _, metric := range metrics {
		if err := addReportUsageMetric(metric.target, metric.value); err != nil {
			return xerrors.Errorf("%s: %w", metric.name, err)
		}
	}
	if record.Cost.State() == domtypes.UsageCostUnavailable {
		row.CostUnavailable++
		return nil
	}
	amount, _ := record.Cost.AmountMicros()
	key := reportUsageCostKey{
		origin: record.Cost.Origin().String(), currency: record.Cost.Currency(),
		priceTableVersion: record.Cost.PriceTableVersion(),
	}
	cost := accumulator.costs[key]
	if cost == nil {
		cost = &apptypes.ReportUsageCostRow{
			Origin: key.origin, Currency: key.currency, PriceTableVersion: key.priceTableVersion,
		}
		accumulator.costs[key] = cost
	}
	cost.Observations++
	total, err := checkedReportSum(cost.AmountMicros, amount)
	if err != nil {
		return xerrors.Errorf("cost amount: %w", err)
	}
	cost.AmountMicros = total
	return nil
}

func addReportUsageMetric(metric *apptypes.ReportUsageMetric, value domtypes.UsageValue) error {
	if numeric, present := value.Value(); present {
		metric.KnownObservations++
		total, err := checkedReportSum(metric.Sum, numeric)
		if err != nil {
			return err
		}
		metric.Sum = total
		return nil
	}
	metric.UnavailableObservations++
	return nil
}

func accumulateReportUsageRun(
	accumulator *reportUsageRunAccumulator,
	record apptypes.ReportUsageRecord,
) error {
	row := &accumulator.row
	row.Runs++
	if err := addReportUsageRunMetric(&row.PacketBytes, record.PacketBytes); err != nil {
		return xerrors.Errorf("packet bytes: %w", err)
	}
	if err := addReportUsageRunMetric(&row.ToolOutputBytes, record.ToolOutputBytes); err != nil {
		return xerrors.Errorf("tool output bytes: %w", err)
	}
	row.WallTimeMS.UnavailableRuns++
	return nil
}

func addReportUsageRunMetric(metric *apptypes.ReportUsageRunMetric, value domtypes.Optional[int64]) error {
	if numeric, present := value.Value(); present {
		metric.KnownRuns++
		total, err := checkedReportSum(metric.Sum, numeric)
		if err != nil {
			return err
		}
		metric.Sum = total
		return nil
	}
	metric.UnavailableRuns++
	return nil
}

func checkedReportSum(current, delta int64) (int64, error) {
	if current < 0 || delta < 0 {
		return 0, xerrors.New("report usage sum requires non-negative values")
	}
	if current > math.MaxInt64-delta {
		return 0, xerrors.New("report usage sum overflows int64")
	}
	return current + delta, nil
}

func sortedReportUsageCosts(
	values map[reportUsageCostKey]*apptypes.ReportUsageCostRow,
) []apptypes.ReportUsageCostRow {
	result := make([]apptypes.ReportUsageCostRow, 0, len(values))
	for _, value := range values {
		result = append(result, *value)
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].Origin + "\x00" + result[i].Currency + "\x00" + result[i].PriceTableVersion
		right := result[j].Origin + "\x00" + result[j].Currency + "\x00" + result[j].PriceTableVersion
		return left < right
	})
	return result
}

func reportUsageAggregateOrder(row apptypes.ReportUsageAggregateRow) string {
	pullRequest := ""
	if row.PullRequest != nil {
		pullRequest = strconv.FormatInt(*row.PullRequest, 10)
	}
	return strings.Join([]string{
		row.Engine, row.Provider, row.Model, row.Repository, row.TicketRef, pullRequest, row.BatchID,
	}, "\x00")
}

func reportUsageRunOrder(row apptypes.ReportUsageRunAggregateRow) string {
	pullRequest := ""
	if row.PullRequest != nil {
		pullRequest = strconv.FormatInt(*row.PullRequest, 10)
	}
	return strings.Join([]string{
		row.Engine, row.Repository, row.TicketRef, pullRequest, row.BatchID,
	}, "\x00")
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
