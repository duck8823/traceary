package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	appusecase "github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// newReportCommand builds `traceary report`, a period-scoped retrospective
// digest. Exit code is always 0 on successful aggregation (health verdicts
// remain doctor's job).
func (c *RootCLI) newReportCommand() *cobra.Command {
	var (
		dbPath    string
		workspace string
		from      string
		since     string
		to        string
		until     string
		client    string
		limit     int
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "report",
		Short: Localize("Period-scoped retrospective digest (sessions, coverage, failures, top commands)", "期間指定の振り返りダイジェスト（sessions / coverage / failures / top commands）"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runReport(cmd.Context(), cmd.OutOrStdout(), reportCommandInput{
				dbPath:    dbPath,
				workspace: workspace,
				from:      from,
				since:     since,
				to:        to,
				until:     until,
				client:    client,
				limit:     limit,
				asJSON:    asJSON,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&workspace, "workspace", "", Localize("workspace filter (default: git detection)", "workspace フィルタ（既定: git 検出）"))
	cmd.Flags().StringVar(&from, "from", "", Localize("period start (inclusive)", "期間開始（inclusive）"))
	cmd.Flags().StringVar(&since, "since", "", Localize("alias for --from", "--from の alias"))
	cmd.Flags().StringVar(&to, "to", "", Localize("period end (exclusive)", "期間終了（exclusive）"))
	cmd.Flags().StringVar(&until, "until", "", Localize("alias for --to", "--to の alias"))
	cmd.Flags().StringVar(&client, "client", "", Localize("optional client filter", "任意の client フィルタ"))
	cmd.Flags().IntVar(&limit, "limit", 5000, Localize("maximum events to scan for aggregation", "集計に使う event の最大件数"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("emit JSON", "JSON で出力する"))
	cmd.AddCommand(c.newWorkspaceIdentityReportCommand())
	return cmd
}

type reportCommandInput struct {
	dbPath    string
	workspace string
	from      string
	since     string
	to        string
	until     string
	client    string
	limit     int
	asJSON    bool
}

type reportEnvelope struct {
	Period           reportPeriod        `json:"period"`
	Workspace        string              `json:"workspace,omitempty"`
	ClientFilter     string              `json:"client,omitempty"`
	Sessions         []reportSessionRow  `json:"sessions"`
	CaptureCoverage  []reportCoverageRow `json:"capture_coverage"`
	Failures         reportFailures      `json:"failures"`
	TopCommands      []reportCommandRow  `json:"top_commands"`
	FailureLoops     []reportFailureLoop `json:"failure_loops,omitempty"`
	EventScanCount   int                 `json:"event_scan_count"`
	SessionScanCount int                 `json:"session_scan_count"`
}

type reportPeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type reportSessionRow struct {
	Client       string `json:"client"`
	Sessions     int    `json:"sessions"`
	TotalEvents  int    `json:"total_events"`
	CommandCount int    `json:"command_count"`
}

type reportCoverageRow struct {
	Client                       string  `json:"client"`
	Sessions                     int     `json:"sessions"`
	WithPrompt                   int     `json:"with_prompt"`
	WithTranscript               int     `json:"with_transcript"`
	WithCommand                  int     `json:"with_command"`
	PromptTranscriptMissing      int     `json:"prompt_transcript_missing"`
	PromptTranscriptMissingRatio float64 `json:"prompt_transcript_missing_ratio"`
}

type reportFailures struct {
	Total    int            `json:"total"`
	ByClient map[string]int `json:"by_client"`
	ByReason map[string]int `json:"by_reason"`
	Samples  []string       `json:"sample_event_ids"`
}

type reportCommandRow struct {
	Command       string  `json:"command"`
	Count         int     `json:"count"`
	FailedCount   int     `json:"failed_count"`
	FailureRate   float64 `json:"failure_rate"`
	SampleEventID string  `json:"sample_event_id,omitempty"`
}

type reportFailureLoop struct {
	Command        string   `json:"command"`
	Workspace      string   `json:"workspace,omitempty"`
	Agent          string   `json:"agent,omitempty"`
	Count          int      `json:"count"`
	SampleEventIDs []string `json:"sample_event_ids"`
}

func (c *RootCLI) runReport(ctx context.Context, output io.Writer, input reportCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.New(Localize("event usecase is not configured", "event usecase が設定されていません"))
	}
	if c.session == nil {
		return xerrors.New(Localize("session usecase is not configured", "session usecase が設定されていません"))
	}
	if c.reportCommand == nil {
		return xerrors.New(Localize("report command usecase is not configured", "report command usecase が設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.New(Localize("--limit must be positive", "--limit は 1 以上である必要があります"))
	}

	fromValue, err := resolveSearchDateValue(input.from, input.since, "from", "since")
	if err != nil {
		return err
	}
	toValue, err := resolveSearchDateValue(input.to, input.until, "to", "until")
	if err != nil {
		return err
	}
	// Default period: last 7 days when --from omitted.
	if strings.TrimSpace(fromValue) == "" && strings.TrimSpace(toValue) == "" {
		now := time.Now().UTC()
		fromValue = now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
		toValue = now.Format(time.RFC3339)
	} else if strings.TrimSpace(fromValue) == "" {
		// If only --to is set, still require an explicit from for clarity.
		return xerrors.New(Localize("--from is required when --to is set (or omit both for last 7 days)", "--to 指定時は --from が必要です（両方省略で直近7日）"))
	} else if strings.TrimSpace(toValue) == "" {
		toValue = time.Now().UTC().Format(time.RFC3339)
	}

	fromRaw, err := parseFlexibleTime(fromValue, false)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve --from", "from の解決に失敗しました"), err)
	}
	toRaw, err := parseFlexibleTime(toValue, false)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve --to", "to の解決に失敗しました"), err)
	}
	if !fromRaw.IsZero() && !toRaw.IsZero() && !fromRaw.Before(toRaw) {
		return xerrors.New(Localize("--from must be earlier than --to", "from は to より前である必要があります"))
	}
	toExclusive, err := parseFlexibleTime(toValue, true)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve --to", "to の解決に失敗しました"), err)
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	ws := types.Workspace(resolveWorkspaceValue(ctx, input.workspace))
	clientFilter := strings.TrimSpace(input.client)

	sessionCriteria := apptypes.NewSessionListCriteriaBuilder(input.limit).
		Workspace(ws).
		Client(types.Client(clientFilter)).
		From(types.Some(fromRaw)).
		To(types.Some(toExclusive)).
		Build()
	sessions, err := c.session.List(ctx, sessionCriteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list sessions for report", "report 用 session 一覧の取得に失敗しました"), err)
	}

	eventCriteria := apptypes.NewEventListCriteriaBuilder(input.limit).
		Workspace(ws).
		Client(types.Client(clientFilter)).
		From(fromRaw).
		To(toExclusive).
		Build()
	events, err := c.event.ListWindow(ctx, eventCriteria)
	if err != nil {
		// Fall back to List when ListWindow is unavailable on stub/older paths.
		events, err = c.event.List(ctx, eventCriteria)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to list events for report", "report 用 event 一覧の取得に失敗しました"), err)
		}
	}
	commandSummary, err := c.reportCommand.Summarize(ctx, eventCriteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to summarize command audits for report", "report 用 command audit の集計に失敗しました"), err)
	}

	envelope := buildReportEnvelope(fromRaw, toExclusive, ws.String(), clientFilter, sessions, events, commandSummary)
	if input.asJSON {
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		if err := enc.Encode(envelope); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode report JSON", "report JSON の encode に失敗しました"), err)
		}
		return nil
	}
	return writeReportText(output, envelope)
}

func buildReportEnvelope(
	from, to time.Time,
	workspace, clientFilter string,
	sessions []apptypes.SessionSummary,
	events []*model.Event,
	commandSummary apptypes.ReportCommandSummary,
) reportEnvelope {
	sessionRows := map[string]*reportSessionRow{}
	for _, s := range sessions {
		client := s.Client().String()
		if client == "" {
			client = "(empty)"
		}
		row := sessionRows[client]
		if row == nil {
			row = &reportSessionRow{Client: client}
			sessionRows[client] = row
		}
		row.Sessions++
		row.TotalEvents += s.TotalEvents()
		row.CommandCount += s.CommandCount()
	}

	coverageByClient := map[string][]appusecase.EventCoverageInput{}
	for _, event := range events {
		if event == nil {
			continue
		}
		client := event.Client().String()
		if client == "" {
			client = "(empty)"
		}
		coverageByClient[client] = append(coverageByClient[client], appusecase.EventCoverageInput{
			SessionID: event.SessionID().String(),
			Kind:      event.Kind(),
		})
	}

	sessionOut := make([]reportSessionRow, 0, len(sessionRows))
	for _, row := range sessionRows {
		sessionOut = append(sessionOut, *row)
	}
	sort.Slice(sessionOut, func(i, j int) bool {
		if sessionOut[i].Sessions == sessionOut[j].Sessions {
			return sessionOut[i].Client < sessionOut[j].Client
		}
		return sessionOut[i].Sessions > sessionOut[j].Sessions
	})

	coverageOut := make([]reportCoverageRow, 0, len(coverageByClient))
	for client, inputs := range coverageByClient {
		summary := appusecase.SummarizeSessionEventCoverage(inputs)
		coverageOut = append(coverageOut, reportCoverageRow{
			Client:                       client,
			Sessions:                     summary.Sessions,
			WithPrompt:                   summary.WithPrompt,
			WithTranscript:               summary.WithTranscript,
			WithCommand:                  summary.WithCommand,
			PromptTranscriptMissing:      summary.PromptTranscriptMissing,
			PromptTranscriptMissingRatio: summary.PromptTranscriptMissingRatio(),
		})
	}
	sort.Slice(coverageOut, func(i, j int) bool { return coverageOut[i].Client < coverageOut[j].Client })

	topCommands := make([]reportCommandRow, 0, len(commandSummary.TopCommands))
	for _, row := range commandSummary.TopCommands {
		topCommands = append(topCommands, reportCommandRow{
			Command: row.Command, Count: row.Count, FailedCount: row.FailedCount,
			FailureRate: row.FailureRate, SampleEventID: row.SampleEventID,
		})
	}
	loops := make([]reportFailureLoop, 0, len(commandSummary.FailureLoops))
	for _, loop := range commandSummary.FailureLoops {
		loops = append(loops, reportFailureLoop{
			Command: loop.Command, Workspace: loop.Workspace, Agent: loop.Agent,
			Count: loop.Count, SampleEventIDs: loop.SampleEventIDs,
		})
	}

	return reportEnvelope{
		Period: reportPeriod{
			From: from.UTC().Format(time.RFC3339),
			To:   to.UTC().Format(time.RFC3339),
		},
		Workspace:        workspace,
		ClientFilter:     clientFilter,
		Sessions:         sessionOut,
		CaptureCoverage:  coverageOut,
		Failures:         reportFailures{Total: commandSummary.FailureTotal, ByClient: commandSummary.FailuresByClient, ByReason: commandSummary.FailuresByReason, Samples: commandSummary.FailureSamples},
		TopCommands:      topCommands,
		FailureLoops:     loops,
		EventScanCount:   len(events),
		SessionScanCount: len(sessions),
	}
}

func writeReportText(output io.Writer, report reportEnvelope) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Traceary report\nPeriod: %s → %s (exclusive end)\n", report.Period.From, report.Period.To)
	if report.Workspace != "" {
		fmt.Fprintf(&b, "Workspace: %s\n", report.Workspace)
	}
	if report.ClientFilter != "" {
		fmt.Fprintf(&b, "Client: %s\n", report.ClientFilter)
	}
	fmt.Fprintf(&b, "Scanned: %d sessions, %d events\n\n", report.SessionScanCount, report.EventScanCount)
	b.WriteString("## sessions\n")
	if len(report.Sessions) == 0 {
		b.WriteString("(none)\n")
	}
	for _, row := range report.Sessions {
		fmt.Fprintf(&b, "- %s: sessions=%d events=%d commands=%d\n", row.Client, row.Sessions, row.TotalEvents, row.CommandCount)
	}
	b.WriteString("\n## capture_coverage\n")
	for _, row := range report.CaptureCoverage {
		fmt.Fprintf(&b, "- %s: sessions=%d prompt=%d transcript=%d command=%d missing_ratio=%.2f\n",
			row.Client, row.Sessions, row.WithPrompt, row.WithTranscript, row.WithCommand, row.PromptTranscriptMissingRatio)
	}
	b.WriteString("\n## failures\n")
	fmt.Fprintf(&b, "total=%d\n", report.Failures.Total)
	for client, n := range report.Failures.ByClient {
		fmt.Fprintf(&b, "- %s: %d\n", client, n)
	}
	for reason, n := range report.Failures.ByReason {
		fmt.Fprintf(&b, "- reason=%s: %d\n", reason, n)
	}
	if len(report.Failures.Samples) > 0 {
		fmt.Fprintf(&b, "samples: %s\n", strings.Join(report.Failures.Samples, ", "))
	}
	if len(report.FailureLoops) > 0 {
		b.WriteString("\n## failure_loops\n")
		for _, loop := range report.FailureLoops {
			fmt.Fprintf(&b, "- %s (agent=%s workspace=%s) count=%d samples=%s\n",
				loop.Command, loop.Agent, loop.Workspace, loop.Count, strings.Join(loop.SampleEventIDs, ","))
		}
	}
	b.WriteString("\n## top_commands\n")
	for _, row := range report.TopCommands {
		fmt.Fprintf(&b, "- %s: count=%d failed=%d rate=%.2f sample=%s\n",
			row.Command, row.Count, row.FailedCount, row.FailureRate, row.SampleEventID)
	}
	if _, err := io.WriteString(output, b.String()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write report text", "report テキストの書き出しに失敗しました"), err)
	}
	return nil
}
