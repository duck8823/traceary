package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

const (
	exactRedeliveryRateTarget          = 0.01
	defaultWorkspaceHeuristicScanLimit = 5000
)

type workspaceHeuristicMeasurementState string

const (
	workspaceHeuristicNotRequested workspaceHeuristicMeasurementState = "not_requested"
	workspaceHeuristicPartial      workspaceHeuristicMeasurementState = "partial"
	workspaceHeuristicComplete     workspaceHeuristicMeasurementState = "complete"
	workspaceHeuristicFailed       workspaceHeuristicMeasurementState = "failed"
)

type workspaceIdentityReportEnvelope struct {
	Workspace     apptypes.WorkspaceIdentityReport   `json:"workspace_identity"`
	ExactDelivery workspaceExactDeliverySummary      `json:"exact_delivery"`
	Heuristic     workspaceHeuristicDuplicateSummary `json:"heuristic_candidates"`
}

type workspaceExactDeliverySummary struct {
	AttemptCount         int     `json:"attempt_count"`
	ExactRedeliveryCount int     `json:"exact_redelivery_count"`
	ExactRedeliveryRate  float64 `json:"exact_redelivery_rate"`
	TargetRate           float64 `json:"target_rate"`
	SampleAvailable      bool    `json:"sample_available"`
	TargetMet            bool    `json:"target_met"`
}

type workspaceHeuristicDuplicateSummary struct {
	MeasurementState workspaceHeuristicMeasurementState      `json:"measurement_state"`
	EligibleCount    int                                     `json:"eligible_count"`
	ScannedCount     int                                     `json:"scanned_count"`
	CandidateCount   int                                     `json:"candidate_count"`
	CandidateRate    float64                                 `json:"candidate_rate"`
	Sources          []apptypes.ContentEventDedupeSourceStat `json:"sources"`
	Error            string                                  `json:"error,omitempty"`
}

type workspaceIdentityReportInput struct {
	dbPath              string
	conflictSampleLimit int
	includeHeuristic    bool
	heuristicLimit      int
	strict              bool
	asJSON              bool
}

func (c *RootCLI) newWorkspaceIdentityReportCommand() *cobra.Command {
	input := workspaceIdentityReportInput{heuristicLimit: defaultWorkspaceHeuristicScanLimit}
	cmd := &cobra.Command{
		Use:   "workspace-identity",
		Short: Localize("Report workspace attribution and delivery identity quality", "workspace attribution と delivery identity の品質を報告する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			// `--strict` was the only explicit heuristic control before the
			// dedicated flag existed. Treat either advanced heuristic flag as an
			// explicit request so existing scripts do not silently lose data.
			if cmd.Flags().Changed("strict") || cmd.Flags().Changed("heuristic-limit") {
				input.includeHeuristic = true
			}
			return c.runWorkspaceIdentityReport(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().IntVar(&input.conflictSampleLimit, "conflict-sample-limit", 20, Localize("maximum body-free conflict samples", "本文を含まない conflict sample の最大件数"))
	cmd.Flags().BoolVar(&input.includeHeuristic, "include-heuristic", false, Localize("measure a bounded sample of historical body/time-window duplicate candidates", "履歴上の本文・時間窓重複候補を上限付きで測定する"))
	cmd.Flags().IntVar(&input.heuristicLimit, "heuristic-limit", defaultWorkspaceHeuristicScanLimit, Localize("maximum prompt/transcript bodies to scan when heuristic measurement is requested", "ヒューリスティック測定時に検査する prompt/transcript 本文の最大件数"))
	cmd.Flags().BoolVar(&input.strict, "strict", false, Localize("measure every exact historical content match within the bounded sample", "上限付きサンプル内で時間差に関係なく完全一致する content を測定する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("emit machine-readable JSON", "機械可読な JSON を出力する"))
	return cmd
}

func (c *RootCLI) runWorkspaceIdentityReport(ctx context.Context, output io.Writer, input workspaceIdentityReportInput) error {
	if c.workspaceIdentity == nil {
		return xerrors.Errorf("%s", Localize("workspace identity usecase is not configured", "workspace identity usecase が設定されていません"))
	}
	if input.conflictSampleLimit < 0 {
		return xerrors.Errorf("%s", Localize("--conflict-sample-limit must not be negative", "--conflict-sample-limit は 0 以上である必要があります"))
	}
	if input.includeHeuristic && input.heuristicLimit <= 0 {
		return xerrors.Errorf("%s", Localize("--heuristic-limit must be positive", "--heuristic-limit は 1 以上である必要があります"))
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if _, err := os.Stat(resolvedDBPath); err != nil {
		if os.IsNotExist(err) {
			return xerrors.Errorf("%s", Localize("store does not exist; run traceary doctor to initialize it before reporting", "store が存在しません。report の前に traceary doctor で初期化してください"))
		}
		return xerrors.Errorf("%s: %w", Localize("failed to inspect DB path", "DB パスを確認できませんでした"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	identity, err := c.workspaceIdentity.Report(ctx, input.conflictSampleLimit)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build workspace identity report; run traceary doctor if the store requires migration", "workspace identity report の作成に失敗しました。store の migration が必要な場合は traceary doctor を実行してください"), err)
	}
	heuristic := workspaceHeuristicDuplicateSummary{
		MeasurementState: workspaceHeuristicNotRequested,
		Sources:          []apptypes.ContentEventDedupeSourceStat{},
	}
	var heuristicErr error
	if input.includeHeuristic {
		if c.storeManagement == nil {
			heuristicErr = xerrors.Errorf("store management usecase is not configured")
		} else {
			result, measureErr := c.storeManagement.DedupeContentEvents(ctx, apptypes.ContentEventDedupeParams{
				Strict:      input.strict,
				MaxScanRows: input.heuristicLimit,
			})
			if measureErr != nil {
				heuristicErr = measureErr
			} else {
				heuristic = buildWorkspaceHeuristicDuplicateSummary(result)
			}
		}
		if heuristicErr != nil {
			heuristic = workspaceHeuristicDuplicateSummary{
				MeasurementState: workspaceHeuristicFailed,
				Sources:          []apptypes.ContentEventDedupeSourceStat{},
				Error:            Localize("measurement failed", "測定に失敗しました"),
			}
		}
	}
	envelope := buildWorkspaceIdentityReportEnvelope(identity, heuristic)
	if input.asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(envelope); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode workspace identity report", "workspace identity report の JSON 化に失敗しました"), err)
		}
	} else if err := writeWorkspaceIdentityReportText(output, envelope); err != nil {
		return err
	}
	if heuristicErr != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to measure historical duplicate candidates", "履歴上の重複候補を測定できませんでした"), heuristicErr)
	}
	return nil
}

func buildWorkspaceIdentityReportEnvelope(identity apptypes.WorkspaceIdentityReport, heuristic workspaceHeuristicDuplicateSummary) workspaceIdentityReportEnvelope {
	attempts, exact := 0, 0
	for _, source := range identity.Sources {
		attempts += source.RuntimeAttemptCount
		exact += source.ExactRedeliveryCount
	}
	return workspaceIdentityReportEnvelope{
		Workspace: identity,
		ExactDelivery: workspaceExactDeliverySummary{
			AttemptCount: attempts, ExactRedeliveryCount: exact,
			ExactRedeliveryRate: reportRatio(exact, attempts),
			TargetRate:          exactRedeliveryRateTarget,
			SampleAvailable:     attempts > 0,
			TargetMet:           attempts > 0 && reportRatio(exact, attempts) < exactRedeliveryRateTarget,
		},
		Heuristic: heuristic,
	}
}

func buildWorkspaceHeuristicDuplicateSummary(result apptypes.ContentEventDedupeResult) workspaceHeuristicDuplicateSummary {
	state := workspaceHeuristicComplete
	if result.ScannedCount < result.TotalEligibleCount {
		state = workspaceHeuristicPartial
	}
	candidates := result.MovedCount()
	return workspaceHeuristicDuplicateSummary{
		MeasurementState: state,
		EligibleCount:    result.TotalEligibleCount,
		ScannedCount:     result.ScannedCount,
		CandidateCount:   candidates,
		CandidateRate:    reportRatio(candidates, result.ScannedCount),
		Sources:          result.Sources,
	}
}

func writeWorkspaceIdentityReportText(output io.Writer, report workspaceIdentityReportEnvelope) error {
	c := report.Workspace.Coverage
	if _, err := fmt.Fprintf(output, "Workspace identity: events=%d covered=%d missing=%d coverage=%.4f observations=%d\n", c.EventCount, c.CoveredEvents, c.MissingEvents, c.CoverageRate, c.ObservationCount); err != nil {
		return xerrors.Errorf("failed to print workspace identity coverage: %w", err)
	}
	if _, err := fmt.Fprintf(output, "Exact delivery: runtime_attempts=%d exact_redeliveries=%d rate=%.4f target<%.4f sample_available=%t target_met=%t\n", report.ExactDelivery.AttemptCount, report.ExactDelivery.ExactRedeliveryCount, report.ExactDelivery.ExactRedeliveryRate, report.ExactDelivery.TargetRate, report.ExactDelivery.SampleAvailable, report.ExactDelivery.TargetMet); err != nil {
		return xerrors.Errorf("failed to print exact delivery summary: %w", err)
	}
	if _, err := fmt.Fprintf(output, "Heuristic content candidates: state=%s eligible=%d scanned=%d candidates=%d rate=%.4f (not proven redeliveries)\n", report.Heuristic.MeasurementState, report.Heuristic.EligibleCount, report.Heuristic.ScannedCount, report.Heuristic.CandidateCount, report.Heuristic.CandidateRate); err != nil {
		return xerrors.Errorf("failed to print heuristic candidate summary: %w", err)
	}
	for _, source := range report.Workspace.Sources {
		if _, err := fmt.Fprintf(output, "  source client=%s hook=%s observations=%d exact=%d descendant=%d ancestor=%d alias=%d conflict=%d unknown=%d conflict_rate=%.4f delivery_attempts=%d runtime_attempts=%d backfilled_attempts=%d exact_redeliveries=%d exact_rate=%.4f\n",
			source.Client, emptyDash(source.SourceHook), source.ObservationCount,
			source.Relationships.Exact, source.Relationships.Descendant, source.Relationships.Ancestor,
			source.Relationships.ExplicitAlias, source.Relationships.Conflict, source.Relationships.Unknown,
			source.ConflictRate, source.DeliveryAttemptCount, source.RuntimeAttemptCount, source.BackfilledAttemptCount, source.ExactRedeliveryCount, source.ExactRedeliveryRate); err != nil {
			return xerrors.Errorf("failed to print workspace identity source: %w", err)
		}
	}
	for _, sample := range report.Workspace.ConflictSamples {
		if _, err := fmt.Fprintf(output, "  conflict event_id=%s session_id=%s client=%s source_hook=%s\n", sample.EventID, sample.SessionID, sample.Client, emptyDash(sample.SourceHook)); err != nil {
			return xerrors.Errorf("failed to print workspace conflict sample: %w", err)
		}
	}
	return nil
}

func reportRatio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
