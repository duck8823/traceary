package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

const mechanicalSummaryMarker = "Mechanical summary (degraded=1)."

const foldGateWorthFoldingRule = "has session_refinements row OR SUM(event_metadata_projection.body_stored_bytes) >= consolidation.threshold_bytes"

// FoldGateInspector measures the #1879 fold / wake rows from metadata tables.
// It does not read event bodies.
type FoldGateInspector struct {
	db *Database
}

// NewFoldGateInspector creates a metadata-only fold-gate inspector.
func NewFoldGateInspector(db *Database) *FoldGateInspector {
	return &FoldGateInspector{db: db}
}

// InspectFoldGate returns refinement ratio, per-client wake eligibility, and a
// structural sample of agent summaries.
func (i *FoldGateInspector) InspectFoldGate(ctx context.Context, thresholdBytes, wakeBudgetBytes int64) (_ apptypes.FoldGateReport, err error) {
	if thresholdBytes <= 0 {
		thresholdBytes = application.DefaultFoldThresholdBytes
	}
	if wakeBudgetBytes <= 0 {
		wakeBudgetBytes = application.DefaultFoldWakeBudgetBytes
	}
	report := apptypes.FoldGateReport{
		SchemaVersion:    "traceary.fold_gate/v1",
		ThresholdBytes:   thresholdBytes,
		WakeBudgetBytes:  wakeBudgetBytes,
		WorthFoldingRule: foldGateWorthFoldingRule,
		Evidence: apptypes.FoldGateEvidence{
			Status: "skipped",
			Method: "event_metadata_projection",
		},
		Content: apptypes.FoldGateContent{SampleLimit: application.FoldGateContentSampleLimit},
	}
	db, err := i.db.openReadOnly(ctx)
	if err != nil {
		return report, xerrors.Errorf("failed to open store for fold-gate: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close fold-gate inspection: %w", closeErr)
		}
	}()

	var projectionExists, refinementExists, sessionExists int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='event_metadata_projection'`).Scan(&projectionExists); err != nil {
		return report, xerrors.Errorf("failed to inspect event metadata schema: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='session_refinements'`).Scan(&refinementExists); err != nil {
		return report, xerrors.Errorf("failed to inspect session refinements schema: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='sessions'`).Scan(&sessionExists); err != nil {
		return report, xerrors.Errorf("failed to inspect sessions schema: %w", err)
	}
	if projectionExists == 0 || refinementExists == 0 || sessionExists == 0 {
		report.Evidence.Reason = "required fold-gate tables unavailable"
		report.RefinementGate = "unmeasured"
		report.WakeGate = "unmeasured"
		return report, nil
	}

	if err := db.QueryRowContext(ctx, `
SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN has_refinement = 1 OR stored_bytes >= ? THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN has_refinement = 1 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN has_agent_reasoning = 1 THEN 1 ELSE 0 END), 0)
FROM (
  SELECT
    ids.session_id,
    COALESCE(bytes.stored_bytes, 0) AS stored_bytes,
    CASE WHEN r.session_id IS NOT NULL THEN 1 ELSE 0 END AS has_refinement,
    COALESCE(r.has_agent_reasoning, 0) AS has_agent_reasoning
  FROM (
    SELECT session_id FROM event_metadata_projection
    UNION
    SELECT session_id FROM session_refinements
    UNION
    SELECT session_id FROM sessions
  ) AS ids
  LEFT JOIN (
    SELECT session_id, COALESCE(SUM(body_stored_bytes), 0) AS stored_bytes
      FROM event_metadata_projection
     GROUP BY session_id
  ) AS bytes ON bytes.session_id = ids.session_id
  LEFT JOIN session_refinements r ON r.session_id = ids.session_id
)`, thresholdBytes).Scan(&report.SessionCount, &report.WorthFoldingCount, &report.RefinementCount, &report.AgentRefinementCount); err != nil {
		return report, xerrors.Errorf("failed to aggregate fold-gate sessions: %w", err)
	}
	if report.WorthFoldingCount > 0 {
		report.RefinementRatio = float64(report.RefinementCount) / float64(report.WorthFoldingCount)
	}
	report.RefinementGate = "miss"
	if report.WorthFoldingCount > 0 && report.RefinementRatio+1e-12 >= application.FoldGateTargetRatio {
		report.RefinementGate = "pass"
	}
	if report.WorthFoldingCount == 0 {
		report.RefinementGate = "unmeasured"
	}

	wake, err := readFoldGateWake(ctx, db, wakeBudgetBytes)
	if err != nil {
		return report, err
	}
	report.Wake = wake
	report.WakeGate = wakeGateStatus(wake)

	content, err := readFoldGateContent(ctx, db)
	if err != nil {
		return report, err
	}
	report.Content = content
	report.Evidence.Status = "complete"
	return report, nil
}

func readFoldGateWake(ctx context.Context, db *sql.DB, budget int64) ([]apptypes.FoldGateWakeHost, error) {
	rows, err := db.QueryContext(ctx, `
SELECT COALESCE(NULLIF(s.client, ''), 'unknown'), r.summary
FROM sessions s
INNER JOIN session_refinements r ON r.session_id = s.session_id
WHERE (s.parent_session_id IS NULL OR s.parent_session_id = '')
  AND r.has_agent_reasoning = 1
ORDER BY 1`)
	if err != nil {
		return nil, xerrors.Errorf("failed to aggregate wake eligibility: %w", err)
	}
	defer func() { _ = rows.Close() }()
	byClient := map[string]*apptypes.FoldGateWakeHost{}
	var order []string
	for rows.Next() {
		var client, summary string
		if err := rows.Scan(&client, &summary); err != nil {
			return nil, xerrors.Errorf("failed to scan wake host: %w", err)
		}
		host, ok := byClient[client]
		if !ok {
			host = &apptypes.FoldGateWakeHost{Client: client}
			byClient[client] = host
			order = append(order, client)
		}
		host.EligibleCount++
		if application.WakeSummaryFitsBudget(summary, budget) {
			host.FitsBudgetCount++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate wake hosts: %w", err)
	}
	hosts := make([]apptypes.FoldGateWakeHost, 0, len(order))
	for _, client := range order {
		host := *byClient[client]
		host.InjectionPossible = host.FitsBudgetCount > 0
		host.Status = "no_eligible"
		if host.EligibleCount > 0 && host.InjectionPossible {
			host.Status = "injects"
		}
		if host.EligibleCount > 0 && !host.InjectionPossible {
			host.Status = "over_budget"
		}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

func wakeGateStatus(hosts []apptypes.FoldGateWakeHost) string {
	if len(hosts) == 0 {
		return "unmeasured"
	}
	for _, host := range hosts {
		if host.EligibleCount > 0 && !host.InjectionPossible {
			return "miss"
		}
	}
	return "pass"
}

func readFoldGateContent(ctx context.Context, db *sql.DB) (apptypes.FoldGateContent, error) {
	out := apptypes.FoldGateContent{SampleLimit: application.FoldGateContentSampleLimit}
	rows, err := db.QueryContext(ctx, `
SELECT r.summary
  FROM sessions s
  INNER JOIN session_refinements r ON r.session_id = s.session_id
 WHERE (s.parent_session_id IS NULL OR s.parent_session_id = '')
   AND r.has_agent_reasoning = 1
 ORDER BY ts_norm(s.started_at) DESC, s.session_id DESC
 LIMIT ?`, application.FoldGateContentSampleLimit)
	if err != nil {
		return out, xerrors.Errorf("failed to sample wake summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var summary string
		if err := rows.Scan(&summary); err != nil {
			return out, xerrors.Errorf("failed to scan sampled summary: %w", err)
		}
		out.Sampled++
		trimmed := strings.TrimSpace(summary)
		if trimmed != "" {
			out.Nonempty++
		}
		mechanical := strings.Contains(summary, mechanicalSummaryMarker)
		if mechanical {
			out.MechanicalTemplate++
		}
		if trimmed != "" && !mechanical && len([]byte(trimmed)) >= 40 {
			out.ContentProxyOK++
		}
	}
	if err := rows.Err(); err != nil {
		return out, xerrors.Errorf("failed to iterate sampled summaries: %w", err)
	}
	return out, nil
}
