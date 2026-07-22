package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const defaultReportPageSize = 5000

// newReportCommand builds `traceary report`, a period-scoped retrospective
// digest. Exit code is always 0 on successful aggregation (health verdicts
// remain doctor's job).
func (c *RootCLI) newReportCommand() *cobra.Command {
	var (
		dbPath      string
		workspace   string
		from        string
		since       string
		to          string
		until       string
		client      string
		timezone    string
		pageSize    int
		resultCap   int
		legacyLimit int
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "report",
		Short: Localize("Period-scoped retrospective digest (sessions, coverage, failures, top commands)", "期間指定の振り返りダイジェスト（sessions / coverage / failures / top commands）"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runReport(cmd.Context(), cmd.OutOrStdout(), reportCommandInput{
				dbPath: dbPath, workspace: workspace, from: from, since: since,
				to: to, until: until, client: client, timezone: timezone,
				pageSize: pageSize, resultCap: resultCap, legacyLimit: legacyLimit,
				pageSizeSet: cmd.Flags().Changed("page-size"), legacyLimitSet: cmd.Flags().Changed("limit"),
				asJSON: asJSON,
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
	cmd.Flags().StringVar(&timezone, "timezone", "UTC", Localize("IANA timezone for date-only bounds (default: UTC)", "日付のみの境界に使う IANA タイムゾーン（既定: UTC）"))
	cmd.Flags().IntVar(&pageSize, "page-size", defaultReportPageSize, Localize("internal database page size (does not cap aggregate results)", "DB 内部のページサイズ（集計結果の上限ではありません）"))
	cmd.Flags().IntVar(&resultCap, "result-cap", 0, Localize("maximum rows per aggregate source; 0 means complete aggregation", "集計元ごとの最大行数（0 は全件集計）"))
	cmd.Flags().IntVar(&legacyLimit, "limit", 0, Localize("deprecated alias for --page-size", "--page-size の非推奨 alias"))
	_ = cmd.Flags().MarkDeprecated("limit", Localize("use --page-size; use --result-cap only for an explicit partial aggregate", "--page-size を使ってください。部分集計を明示する場合だけ --result-cap を使います"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("emit JSON", "JSON で出力する"))
	cmd.AddCommand(c.newWorkspaceIdentityReportCommand())
	return cmd
}

type reportCommandInput struct {
	dbPath         string
	workspace      string
	from           string
	since          string
	to             string
	until          string
	client         string
	timezone       string
	pageSize       int
	resultCap      int
	legacyLimit    int
	pageSizeSet    bool
	legacyLimitSet bool
	asJSON         bool
}

func (c *RootCLI) runReport(ctx context.Context, output io.Writer, input reportCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.report == nil {
		return xerrors.New(Localize("report usecase is not configured", "report usecase が設定されていません"))
	}
	if input.pageSizeSet && input.legacyLimitSet {
		return xerrors.New(Localize("--limit and --page-size cannot be used together", "--limit と --page-size は同時に指定できません"))
	}
	if input.legacyLimitSet {
		input.pageSize = input.legacyLimit
	}

	fromValue, err := resolveSearchDateValue(input.from, input.since, "from", "since")
	if err != nil {
		return err
	}
	toValue, err := resolveSearchDateValue(input.to, input.until, "to", "until")
	if err != nil {
		return err
	}
	criteria, err := apptypes.ReportCriteriaFrom(
		fromValue, toValue, input.timezone, time.Now().UTC(),
		types.Workspace(resolveWorkspaceValue(ctx, input.workspace)),
		types.Client(strings.TrimSpace(input.client)),
		input.pageSize, input.resultCap,
	)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve report criteria", "report 条件の解決に失敗しました"), err)
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	report, err := c.report.Generate(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to generate report", "report の生成に失敗しました"), err)
	}
	if input.asJSON {
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode report JSON", "report JSON の encode に失敗しました"), err)
		}
		return nil
	}
	return writeReportText(output, report)
}

func writeReportText(output io.Writer, report apptypes.ReportSnapshot) error {
	var b strings.Builder
	b.WriteString("Traceary report\n")
	requestedFrom := report.Period.RequestedFrom
	if requestedFrom == "" {
		requestedFrom = report.Period.EffectiveFromInclusive
	}
	requestedTo := report.Period.RequestedTo
	if requestedTo == "" {
		requestedTo = report.Period.SnapshotAt
	}
	if report.Period.ToDateOnly {
		fmt.Fprintf(&b, "Period: %s → %s (inclusive calendar end; timezone=%s)\n", requestedFrom, requestedTo, report.Period.Timezone)
	} else {
		fmt.Fprintf(&b, "Period: %s → %s (exclusive instant; timezone=%s)\n", requestedFrom, requestedTo, report.Period.Timezone)
	}
	fmt.Fprintf(&b, "Effective interval: %s → %s (exclusive end; snapshot=%s)\n", report.Period.EffectiveFromInclusive, report.Period.EffectiveToExclusive, report.Period.SnapshotAt)
	fmt.Fprintf(&b, "Aggregation: coverage=%s page_size=%d result_cap=%d\n", report.Aggregation.Coverage, report.Aggregation.PageSize, report.Aggregation.ResultCap)
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
		ratio := "unavailable(partial)"
		if row.PromptTranscriptMissingRatio != nil {
			ratio = fmt.Sprintf("%.2f", *row.PromptTranscriptMissingRatio)
		}
		fmt.Fprintf(&b, "- %s: sessions=%d prompt=%d transcript=%d command=%d missing_ratio=%s\n",
			row.Client, row.Sessions, row.WithPrompt, row.WithTranscript, row.WithCommand, ratio)
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
		rate := "unavailable(partial)"
		if row.FailureRate != nil {
			rate = fmt.Sprintf("%.2f", *row.FailureRate)
		}
		fmt.Fprintf(&b, "- %s: count=%d failed=%d rate=%s sample=%s\n",
			row.Command, row.Count, row.FailedCount, rate, row.SampleEventID)
	}
	if _, err := io.WriteString(output, b.String()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write report text", "report テキストの書き出しに失敗しました"), err)
	}
	return nil
}
