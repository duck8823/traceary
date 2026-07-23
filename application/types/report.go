package types

import (
	"math"
	"strings"
	"time"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	defaultReportWindow = 7 * 24 * time.Hour

	// MaxReportPageSize bounds one internal database page before allocation.
	MaxReportPageSize = 100_000
)

// ReportCoverage identifies whether an aggregate covers the full requested
// filter or an explicitly capped prefix.
type ReportCoverage string

const (
	// ReportCoverageComplete means all matching rows were aggregated.
	ReportCoverageComplete ReportCoverage = "complete"
	// ReportCoveragePartial means result_cap excluded matching rows.
	ReportCoveragePartial ReportCoverage = "partial"
)

// ReportCriteria is the validated, provider-neutral input shared by CLI and
// MCP report surfaces.
type ReportCriteria struct {
	interval  RequestedInterval
	workspace domtypes.Workspace
	client    domtypes.Client
	pageSize  int
	resultCap int
}

// ReportCriteriaFrom validates one report request. Both omitted bounds select
// the default seven-day window while preserving the omitted requested values.
func ReportCriteriaFrom(
	requestedFrom, requestedTo, timezone string,
	snapshotAt time.Time,
	workspace domtypes.Workspace,
	client domtypes.Client,
	pageSize, resultCap int,
) (ReportCriteria, error) {
	if snapshotAt.IsZero() {
		return ReportCriteria{}, xerrors.New("report snapshot must not be zero")
	}
	if pageSize <= 0 {
		return ReportCriteria{}, xerrors.New("page size must be greater than or equal to 1")
	}
	if pageSize > MaxReportPageSize {
		return ReportCriteria{}, xerrors.Errorf("page size must be less than or equal to %d", MaxReportPageSize)
	}
	if resultCap < 0 {
		return ReportCriteria{}, xerrors.New("result cap must be greater than or equal to 0")
	}
	if resultCap == math.MaxInt {
		return ReportCriteria{}, xerrors.New("result cap is too large")
	}

	from := strings.TrimSpace(requestedFrom)
	to := strings.TrimSpace(requestedTo)
	if from == "" && to != "" {
		return ReportCriteria{}, xerrors.New("from is required when to is set")
	}
	interval, err := RequestedIntervalFrom(from, to, timezone, snapshotAt)
	if err != nil {
		return ReportCriteria{}, err
	}
	if from == "" && to == "" {
		interval, err = interval.WithDefaultFrom(snapshotAt.Add(-defaultReportWindow))
		if err != nil {
			return ReportCriteria{}, xerrors.Errorf("failed to apply default report window: %w", err)
		}
	}
	return ReportCriteria{
		interval: interval, workspace: workspace, client: client,
		pageSize: pageSize, resultCap: resultCap,
	}, nil
}

// Interval returns the resolved half-open report interval.
func (c ReportCriteria) Interval() RequestedInterval { return c.interval }

// Workspace returns the optional workspace filter.
func (c ReportCriteria) Workspace() domtypes.Workspace { return c.workspace }

// Client returns the optional client filter.
func (c ReportCriteria) Client() domtypes.Client { return c.client }

// PageSize returns the internal database page size.
func (c ReportCriteria) PageSize() int { return c.pageSize }

// ResultCap returns the per-source row cap, or zero for full aggregation.
func (c ReportCriteria) ResultCap() int { return c.resultCap }

// ReportSessionRecord is the body-free session projection required by report
// aggregation.
type ReportSessionRecord struct {
	Client       domtypes.Client
	StartedAt    time.Time
	TotalEvents  int
	CommandCount int
}

// ReportUsageRecord is one body-free finalized usage projection loaded under
// the report snapshot. Role, round, and wall time are intentionally absent
// because the durable schema does not currently record them.
type ReportUsageRecord struct {
	ObservationID   string
	ObservedAt      time.Time
	Engine          string
	Provider        string
	Model           string
	Accounting      domtypes.UsageAccounting
	TerminalCode    domtypes.UsageTerminalCode
	Counters        domtypes.UsageCounters
	Cost            domtypes.UsageCost
	RunHost         string
	RunID           string
	Repository      string
	TicketRef       string
	PullRequest     domtypes.Optional[int64]
	BatchID         string
	PacketBytes     domtypes.Optional[int64]
	ToolOutputBytes domtypes.Optional[int64]
}

// ReportSourceExtent describes the returned portion of one aggregate source.
// It mirrors response-truncation provenance: a partial source names the cap
// that cut the response and the observed time range.
type ReportSourceExtent struct {
	Coverage           ReportCoverage `json:"coverage" jsonschema:"complete when the full filter was scanned; partial when result_cap truncated the source"`
	ObservedCount      int            `json:"observed_count" jsonschema:"rows included in this aggregate"`
	PageSize           int            `json:"page_size" jsonschema:"internal database page size"`
	ResultCap          int            `json:"result_cap,omitempty" jsonschema:"caller-requested per-source row cap; omitted means unlimited"`
	ResponseTruncated  bool           `json:"response_truncated" jsonschema:"whether rows beyond result_cap were excluded"`
	TruncationReason   string         `json:"truncation_reason,omitempty" jsonschema:"result_cap when response_truncated is true"`
	ObservedEarliestAt string         `json:"observed_earliest_at,omitempty" jsonschema:"earliest included row timestamp (RFC3339Nano)"`
	ObservedLatestAt   string         `json:"observed_latest_at,omitempty" jsonschema:"latest included row timestamp (RFC3339Nano)"`
}

// ReportSourceExtentOf creates validated extent metadata from the included row
// timestamps. Callers pass only returned rows, not the sentinel row used to
// detect truncation.
func ReportSourceExtentOf(observedAt []time.Time, pageSize, resultCap int, truncated bool) (ReportSourceExtent, error) {
	if pageSize <= 0 {
		return ReportSourceExtent{}, xerrors.New("page size must be greater than or equal to 1")
	}
	if pageSize > MaxReportPageSize {
		return ReportSourceExtent{}, xerrors.Errorf("page size must be less than or equal to %d", MaxReportPageSize)
	}
	if resultCap < 0 {
		return ReportSourceExtent{}, xerrors.New("result cap must be greater than or equal to 0")
	}
	if truncated && resultCap == 0 {
		return ReportSourceExtent{}, xerrors.New("truncated report source requires a positive result cap")
	}
	if truncated && len(observedAt) == 0 {
		return ReportSourceExtent{}, xerrors.New("truncated report source requires at least one observed row")
	}
	extent := ReportSourceExtent{
		Coverage: ReportCoverageComplete, ObservedCount: len(observedAt),
		PageSize: pageSize, ResultCap: resultCap,
	}
	if truncated {
		extent.Coverage = ReportCoveragePartial
		extent.ResponseTruncated = true
		extent.TruncationReason = "result_cap"
	}
	var earliest, latest time.Time
	for _, observed := range observedAt {
		if observed.IsZero() {
			return ReportSourceExtent{}, xerrors.New("observed report timestamp must not be zero")
		}
		observed = observed.UTC()
		if earliest.IsZero() || observed.Before(earliest) {
			earliest = observed
		}
		if latest.IsZero() || observed.After(latest) {
			latest = observed
		}
	}
	if !earliest.IsZero() {
		extent.ObservedEarliestAt = earliest.Format(time.RFC3339Nano)
		extent.ObservedLatestAt = latest.Format(time.RFC3339Nano)
	}
	return extent, nil
}

// ReportSourceExtents groups provenance for each aggregate source.
type ReportSourceExtents struct {
	Sessions ReportSourceExtent `json:"sessions"`
	Events   ReportSourceExtent `json:"events"`
	Commands ReportSourceExtent `json:"commands"`
	Usage    ReportSourceExtent `json:"usage"`
}

// ReportWindow is the raw, body-free snapshot returned by report storage.
type ReportWindow struct {
	Sessions []ReportSessionRecord
	Events   []EventMetadata
	Commands []ReportCommandRecord
	Usage    []ReportUsageRecord
	Extents  ReportSourceExtents
}

// ReportAggregation describes overall and per-source aggregate completeness.
type ReportAggregation struct {
	Coverage  ReportCoverage      `json:"coverage" jsonschema:"complete only when all aggregate sources are complete"`
	PageSize  int                 `json:"page_size" jsonschema:"internal database page size"`
	ResultCap int                 `json:"result_cap,omitempty" jsonschema:"caller-requested per-source row cap; omitted means unlimited"`
	Sources   ReportSourceExtents `json:"sources"`
}

// ReportPeriod preserves requested bounds and exposes their resolved interval.
type ReportPeriod struct {
	From                   string `json:"from"`
	To                     string `json:"to"`
	RequestedFrom          string `json:"requested_from"`
	RequestedTo            string `json:"requested_to"`
	EffectiveFromInclusive string `json:"effective_from_inclusive"`
	EffectiveToExclusive   string `json:"effective_to_exclusive"`
	Timezone               string `json:"timezone"`
	SnapshotAt             string `json:"snapshot_at"`
	FromDateOnly           bool   `json:"from_date_only"`
	ToDateOnly             bool   `json:"to_date_only"`
}

// ReportSessionRow is one client-grouped session aggregate.
type ReportSessionRow struct {
	Client       string `json:"client"`
	Sessions     int    `json:"sessions"`
	TotalEvents  int    `json:"total_events"`
	CommandCount int    `json:"command_count"`
}

// ReportCoverageRow is one client-grouped capture coverage aggregate.
type ReportCoverageRow struct {
	Client                       string   `json:"client"`
	Sessions                     int      `json:"sessions"`
	WithPrompt                   int      `json:"with_prompt"`
	WithTranscript               int      `json:"with_transcript"`
	WithCommand                  int      `json:"with_command"`
	PromptTranscriptMissing      int      `json:"prompt_transcript_missing"`
	PromptTranscriptMissingRatio *float64 `json:"prompt_transcript_missing_ratio,omitempty"`
}

// ReportFailures summarizes failed command audits.
type ReportFailures struct {
	Total    int            `json:"total"`
	ByClient map[string]int `json:"by_client"`
	ByReason map[string]int `json:"by_reason"`
	Samples  []string       `json:"sample_event_ids"`
}

// ReportCommandOutput is one normalized command aggregate.
type ReportCommandOutput struct {
	Command       string   `json:"command"`
	Count         int      `json:"count"`
	FailedCount   int      `json:"failed_count"`
	FailureRate   *float64 `json:"failure_rate,omitempty"`
	SampleEventID string   `json:"sample_event_id,omitempty"`
}

// ReportFailureLoopOutput describes one repeated command failure group.
type ReportFailureLoopOutput struct {
	Command        string   `json:"command"`
	Workspace      string   `json:"workspace,omitempty"`
	Agent          string   `json:"agent,omitempty"`
	Count          int      `json:"count"`
	SampleEventIDs []string `json:"sample_event_ids"`
}

// ReportUsageMetric aggregates one usage dimension without treating an
// unavailable value as numeric zero.
type ReportUsageMetric struct {
	KnownObservations       int   `json:"known_observations"`
	UnavailableObservations int   `json:"unavailable_observations"`
	Sum                     int64 `json:"sum"`
}

// ReportUsageRunMetric aggregates immutable run facts exactly once per run.
type ReportUsageRunMetric struct {
	KnownRuns       int   `json:"known_runs"`
	UnavailableRuns int   `json:"unavailable_runs"`
	Sum             int64 `json:"sum"`
}

// ReportUsageCostRow keeps provider-reported and estimated amounts separate.
type ReportUsageCostRow struct {
	Origin            string `json:"origin" jsonschema:"provider_reported or estimated"`
	Currency          string `json:"currency"`
	PriceTableVersion string `json:"price_table_version,omitempty"`
	Observations      int    `json:"observations"`
	AmountMicros      int64  `json:"amount_micros"`
}

// ReportUsageAggregateRow is one comparable provider/run-attribution group.
// Role and round remain explicitly unavailable until their authoritative
// values are persisted.
type ReportUsageAggregateRow struct {
	Provider          string               `json:"provider,omitempty"`
	Engine            string               `json:"engine"`
	Model             string               `json:"model,omitempty"`
	Role              string               `json:"role,omitempty"`
	RoleAvailability  string               `json:"role_availability"`
	Repository        string               `json:"repository,omitempty"`
	TicketRef         string               `json:"ticket,omitempty"`
	PullRequest       *int64               `json:"pull_request,omitempty"`
	BatchID           string               `json:"batch,omitempty"`
	Round             *int64               `json:"round,omitempty"`
	RoundAvailability string               `json:"round_availability"`
	Observations      int                  `json:"observations"`
	Accounted         int                  `json:"accounted_observations"`
	Excluded          int                  `json:"excluded_observations"`
	InputTokens       ReportUsageMetric    `json:"input_tokens"`
	CachedInputTokens ReportUsageMetric    `json:"cached_input_tokens"`
	CacheWriteTokens  ReportUsageMetric    `json:"cache_write_input_tokens"`
	OutputTokens      ReportUsageMetric    `json:"output_tokens"`
	ReasoningTokens   ReportUsageMetric    `json:"reasoning_output_tokens"`
	TotalTokens       ReportUsageMetric    `json:"total_tokens"`
	CostUnavailable   int                  `json:"cost_unavailable_observations"`
	Costs             []ReportUsageCostRow `json:"costs"`
	TerminalCodes     map[string]int       `json:"terminal_classifications"`
}

// ReportUsageRunAggregateRow reports immutable run facts separately from
// provider/model token groups so one run cannot multiply packet or tool bytes
// when it carries multiple usage observations.
type ReportUsageRunAggregateRow struct {
	Engine            string               `json:"engine"`
	Role              string               `json:"role,omitempty"`
	RoleAvailability  string               `json:"role_availability"`
	Repository        string               `json:"repository,omitempty"`
	TicketRef         string               `json:"ticket,omitempty"`
	PullRequest       *int64               `json:"pull_request,omitempty"`
	BatchID           string               `json:"batch,omitempty"`
	Round             *int64               `json:"round,omitempty"`
	RoundAvailability string               `json:"round_availability"`
	Runs              int                  `json:"runs"`
	PacketBytes       ReportUsageRunMetric `json:"packet_bytes"`
	ToolOutputBytes   ReportUsageRunMetric `json:"tool_output_bytes"`
	WallTimeMS        ReportUsageRunMetric `json:"wall_time_ms"`
}

// ReportUsageSnapshot combines observation and run aggregates from the same
// database snapshot.
type ReportUsageSnapshot struct {
	Aggregates []ReportUsageAggregateRow    `json:"aggregates"`
	Runs       []ReportUsageRunAggregateRow `json:"runs"`
}

// ReportSnapshot is the shared CLI/MCP report response.
type ReportSnapshot struct {
	Period           ReportPeriod              `json:"period"`
	Aggregation      ReportAggregation         `json:"aggregation"`
	Workspace        string                    `json:"workspace,omitempty"`
	ClientFilter     string                    `json:"client,omitempty"`
	Sessions         []ReportSessionRow        `json:"sessions"`
	CaptureCoverage  []ReportCoverageRow       `json:"capture_coverage"`
	Failures         ReportFailures            `json:"failures"`
	TopCommands      []ReportCommandOutput     `json:"top_commands"`
	FailureLoops     []ReportFailureLoopOutput `json:"failure_loops,omitempty"`
	Usage            ReportUsageSnapshot       `json:"usage"`
	EventScanCount   int                       `json:"event_scan_count"`
	SessionScanCount int                       `json:"session_scan_count"`
	UsageScanCount   int                       `json:"usage_scan_count"`
}
