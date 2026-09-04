package sqlite

import (
	"context"
	"database/sql"
	"os"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// ReleaseGateEvaluator evaluates the #1873 gate rows on a fixture store.
type ReleaseGateEvaluator struct {
	db *Database
}

// NewReleaseGateEvaluator creates a fixture-store release-gate evaluator.
func NewReleaseGateEvaluator(db *Database) *ReleaseGateEvaluator {
	return &ReleaseGateEvaluator{db: db}
}

// Evaluate runs the remaining #1620 gates. The default live store is refused.
// A miss sets Passed=false; skipped rows do not.
func (e *ReleaseGateEvaluator) Evaluate(ctx context.Context, now time.Time) (_ apptypes.ReleaseGateReport, err error) {
	report := apptypes.ReleaseGateReport{
		SchemaVersion: application.ReleaseGateSchemaVersion,
		Corpus:        application.ReleaseGateMeasurementCorpus,
	}
	if e.db == nil {
		return report, xerrors.Errorf("release-gate evaluator requires a store")
	}
	path := e.db.Path()
	report.StorePath = path
	if err := application.RefuseLiveStore(path); err != nil {
		return report, xerrors.Errorf("failed to evaluate release gates: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var resident int64
	if info, statErr := os.Stat(path); statErr == nil {
		resident = info.Size()
	}

	cost, err := NewOperatorCostInspector(e.db).InspectOperatorCost(ctx, now, resident)
	if err != nil {
		return report, xerrors.Errorf("failed to inspect operator cost: %w", err)
	}
	fold, err := NewFoldGateInspector(e.db).InspectFoldGate(ctx, application.DefaultFoldThresholdBytes, application.DefaultFoldWakeBudgetBytes)
	if err != nil {
		return report, xerrors.Errorf("failed to inspect fold-gate: %w", err)
	}

	db, err := e.db.openReadOnly(ctx)
	if err != nil {
		return report, xerrors.Errorf("failed to open store for release gates: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close release-gate evaluation: %w", closeErr)
		}
	}()

	emission, err := readEmissionAmplification(ctx, db)
	if err != nil {
		return report, err
	}
	duplicate, err := readBodyDuplicateShare(ctx, db)
	if err != nil {
		return report, err
	}
	recent, err := readRecentIndexAmplification(ctx, db)
	if err != nil {
		return report, err
	}

	report.Gates = []apptypes.ReleaseGateResult{
		classifyEmission(emission),
		classifyWholeStore(cost),
		classifyRecentIndex(recent),
		classifyDuplicateShare(duplicate),
		classifyRefinement(fold),
		classifyWake(fold),
	}
	report.Measurements = releaseGateMeasurements(cost, emission)
	report.Passed = true
	for _, gate := range report.Gates {
		if gate.Status == application.ReleaseGateStatusMiss {
			report.Passed = false
			break
		}
	}
	return report, nil
}

type emissionStats struct {
	events    int64
	canonical int64
	measured  bool
	ratio     float64
	promptN   int64
	promptB   int64
	commandN  int64
	commandB  int64
}

type duplicateStats struct {
	measured bool
	share    float64
	total    int64
	distinct int64
}

type recentIndexStats struct {
	measured bool
	ratio    float64
	reason   string
}

func readEmissionAmplification(ctx context.Context, db *sql.DB) (emissionStats, error) {
	var out emissionStats
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='event_metadata_projection'`).Scan(&exists); err != nil {
		return out, xerrors.Errorf("failed to inspect event metadata schema: %w", err)
	}
	if exists == 0 {
		return out, nil
	}
	if err := db.QueryRowContext(ctx, `
SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN kind IN (?, ?) THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN kind = ? THEN COALESCE(body_original_bytes, body_stored_bytes, 0) ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN kind = ? THEN COALESCE(body_original_bytes, body_stored_bytes, 0) ELSE 0 END), 0)
FROM event_metadata_projection`,
		string(types.EventKindPrompt), string(types.EventKindCommandExecuted),
		string(types.EventKindPrompt), string(types.EventKindPrompt),
		string(types.EventKindCommandExecuted), string(types.EventKindCommandExecuted),
	).Scan(&out.events, &out.canonical, &out.promptN, &out.promptB, &out.commandN, &out.commandB); err != nil {
		return out, xerrors.Errorf("failed to aggregate emission amplification: %w", err)
	}
	if out.canonical > 0 {
		out.measured = true
		out.ratio = float64(out.events) / float64(out.canonical)
	}
	return out, nil
}

func readBodyDuplicateShare(ctx context.Context, db *sql.DB) (duplicateStats, error) {
	var out duplicateStats
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='events'`).Scan(&exists); err != nil {
		return out, xerrors.Errorf("failed to inspect events schema: %w", err)
	}
	if exists == 0 {
		return out, nil
	}
	if err := db.QueryRowContext(ctx, `
SELECT
  COALESCE(SUM(COALESCE(body_plaintext_bytes, body_stored_bytes, 0)), 0),
  COALESCE((
    SELECT SUM(sz) FROM (
      SELECT MAX(COALESCE(body_plaintext_bytes, body_stored_bytes, 0)) AS sz
        FROM events
       WHERE body_sha256 IS NOT NULL AND body_sha256 != ''
       GROUP BY body_sha256
    )
  ), 0)
FROM events`).Scan(&out.total, &out.distinct); err != nil {
		return out, xerrors.Errorf("failed to aggregate body duplicate share: %w", err)
	}
	if out.total > 0 {
		out.measured = true
		out.share = float64(out.total-out.distinct) / float64(out.total)
		if out.share < 0 {
			out.share = 0
		}
	}
	return out, nil
}

func readRecentIndexAmplification(_ context.Context, _ *sql.DB) (recentIndexStats, error) {
	return recentIndexStats{reason: "recent index family is no longer stored"}, nil
}

func classifyEmission(stats emissionStats) apptypes.ReleaseGateResult {
	return apptypes.ReleaseGateResult{
		ID:        "emission_amplification",
		Kind:      application.ReleaseGateKindRatio,
		Status:    application.ClassifyUpperBound(stats.measured, stats.ratio, application.ReleaseGateEmissionAmplificationMax),
		Observed:  stats.ratio,
		Threshold: application.ReleaseGateEmissionAmplificationMax,
		Unit:      "events / canonical operation",
		Reason:    skipReason(stats.measured, "no prompt or command_executed rows"),
	}
}

func classifyWholeStore(cost apptypes.OperatorCostReport) apptypes.ReleaseGateResult {
	measured := cost.Evidence.Status == "complete" && cost.RetainedSourceBytes > 0 && cost.Amplification > 0
	return apptypes.ReleaseGateResult{
		ID:        "whole_store_amplification",
		Kind:      application.ReleaseGateKindRatio,
		Status:    application.ClassifyUpperBound(measured, cost.Amplification, application.ReleaseGateWholeStoreAmplificationMax),
		Observed:  cost.Amplification,
		Threshold: application.ReleaseGateWholeStoreAmplificationMax,
		Unit:      "resident bytes / retained source bytes",
		Reason:    skipReason(measured, "operator-cost amplification unmeasured"),
	}
}

func classifyRecentIndex(stats recentIndexStats) apptypes.ReleaseGateResult {
	return apptypes.ReleaseGateResult{
		ID:        "recent_index_amplification",
		Kind:      application.ReleaseGateKindRatio,
		Status:    application.ClassifyUpperBound(stats.measured, stats.ratio, application.ReleaseGateRecentIndexAmplificationMax),
		Observed:  stats.ratio,
		Threshold: application.ReleaseGateRecentIndexAmplificationMax,
		Unit:      "index bytes / recent source bytes",
		Reason:    skipReason(stats.measured, stats.reason),
	}
}

func classifyDuplicateShare(stats duplicateStats) apptypes.ReleaseGateResult {
	return apptypes.ReleaseGateResult{
		ID:        "body_duplicate_share",
		Kind:      application.ReleaseGateKindRatio,
		Status:    application.ClassifyStrictUpperBound(stats.measured, stats.share, application.ReleaseGateBodyDuplicateShareMax),
		Observed:  stats.share,
		Threshold: application.ReleaseGateBodyDuplicateShareMax,
		Unit:      "duplicate plaintext bytes / stored plaintext bytes",
		Reason:    skipReason(stats.measured, "no event body bytes"),
	}
}

func classifyRefinement(fold apptypes.FoldGateReport) apptypes.ReleaseGateResult {
	measured := fold.RefinementGate == "pass" || fold.RefinementGate == "miss"
	status := application.ReleaseGateStatusSkip
	if fold.RefinementGate == "pass" {
		status = application.ReleaseGateStatusPass
	}
	if fold.RefinementGate == "miss" {
		status = application.ReleaseGateStatusMiss
	}
	return apptypes.ReleaseGateResult{
		ID:        "refinement_coverage",
		Kind:      application.ReleaseGateKindCoverage,
		Status:    status,
		Observed:  fold.RefinementRatio,
		Threshold: application.FoldGateTargetRatio,
		Unit:      "refinements / sessions worth folding",
		Reason:    skipReason(measured, "no sessions worth folding"),
	}
}

func classifyWake(fold apptypes.FoldGateReport) apptypes.ReleaseGateResult {
	status := application.ReleaseGateStatusSkip
	if fold.WakeGate == "pass" {
		status = application.ReleaseGateStatusPass
	}
	if fold.WakeGate == "miss" {
		status = application.ReleaseGateStatusMiss
	}
	return apptypes.ReleaseGateResult{
		ID:     "wake_injection",
		Kind:   application.ReleaseGateKindBoolean,
		Status: status,
		Reason: skipReason(status != application.ReleaseGateStatusSkip, "no wake-eligible host"),
	}
}

func skipReason(measured bool, reason string) string {
	if measured {
		return ""
	}
	return reason
}

func releaseGateMeasurements(cost apptypes.OperatorCostReport, emission emissionStats) []apptypes.ReleaseGateMeasurement {
	corpus := application.ReleaseGateMeasurementCorpus
	canonical := float64(emission.canonical)
	undiscardable := 0.0
	resident := 0.0
	if emission.canonical > 0 {
		undiscardable = float64(cost.UndiscardableSourceBytes) / canonical
		resident = float64(cost.ResidentBytes) / canonical
	}
	prompt := 0.0
	if emission.promptN > 0 {
		prompt = float64(emission.promptB) / float64(emission.promptN)
	}
	command := 0.0
	if emission.commandN > 0 {
		command = float64(emission.commandB) / float64(emission.commandN)
	}
	return []apptypes.ReleaseGateMeasurement{
		{ID: "undiscardable_per_canonical_op", Unit: "bytes / canonical operation", ObservedBytesPerUnit: undiscardable, PublishedBound: "<= 5 KiB", Corpus: corpus},
		{ID: "command_record_per_execution", Unit: "bytes / command execution", ObservedBytesPerUnit: command, PublishedBound: "<= 4 KiB", Corpus: corpus},
		{ID: "prompt_record_per_turn", Unit: "bytes / user turn", ObservedBytesPerUnit: prompt, PublishedBound: "<= 12 KiB", Corpus: corpus},
		{ID: "session_tier_per_session", Unit: "bytes / session", ObservedBytesPerUnit: cost.ResidentBytesPerSession, PublishedBound: "<= 64 KiB", Corpus: corpus},
		{ID: "resident_per_canonical_op", Unit: "bytes / canonical operation", ObservedBytesPerUnit: resident, PublishedBound: "<= 13 KiB", Corpus: corpus},
	}
}
