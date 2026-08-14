package sqlite

import (
	"context"
	"os"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

// OperatorCostInspector measures operator-local store cost from the
// event metadata projection. It does not read event bodies.
type OperatorCostInspector struct {
	db *Database
}

// NewOperatorCostInspector creates a projection-backed operator cost inspector.
func NewOperatorCostInspector(db *Database) *OperatorCostInspector {
	return &OperatorCostInspector{db: db}
}

// InspectOperatorCost returns indexed aggregates for this store.
func (i *OperatorCostInspector) InspectOperatorCost(ctx context.Context, now time.Time, residentBytes int64) (_ apptypes.OperatorCostReport, err error) {
	report := apptypes.OperatorCostReport{
		SchemaVersion: "traceary.operator_cost/v1",
		WindowDays:    application.OperatorCostWindowDays,
		ResidentBytes: residentBytes,
		Evidence: apptypes.OperatorCostEvidence{
			Status: "skipped",
			Method: "event_metadata_projection",
		},
	}
	if residentBytes <= 0 && i.db != nil && i.db.Path() != "" {
		if info, statErr := os.Stat(i.db.Path()); statErr == nil {
			report.ResidentBytes = info.Size()
		}
	}
	if now.IsZero() {
		now = time.Now()
	}
	db, err := i.db.openReadOnly(ctx)
	if err != nil {
		return report, xerrors.Errorf("failed to open store for operator cost: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close operator cost inspection: %w", closeErr)
		}
	}()

	var projectionExists int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='event_metadata_projection'`).Scan(&projectionExists); err != nil {
		return report, xerrors.Errorf("failed to inspect event metadata schema: %w", err)
	}
	if projectionExists == 0 {
		report.Evidence.Reason = "event metadata projection unavailable"
		return report, nil
	}

	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*), COUNT(DISTINCT session_id), COALESCE(SUM(COALESCE(body_stored_bytes, 0)), 0)
  FROM event_metadata_projection`).Scan(&report.EventCount, &report.SessionCount, &report.RetainedSourceBytes); err != nil {
		return report, xerrors.Errorf("failed to aggregate retained source bytes: %w", err)
	}

	var refinementsExist int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='session_refinements'`).Scan(&refinementsExist); err != nil {
		return report, xerrors.Errorf("failed to inspect session refinements schema: %w", err)
	}
	cutoff := formatTimestamp(application.CompactCutoff(now, application.DefaultCompactKeepDays))
	if refinementsExist > 0 {
		if err := db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(COALESCE(p.body_stored_bytes, 0)), 0)
  FROM event_metadata_projection AS p
 WHERE p.kind = 'transcript'
   AND p.created_at_norm IS NOT NULL
   AND p.created_at_norm < ?
   AND EXISTS (
         SELECT 1 FROM session_refinements AS r WHERE r.session_id = p.session_id
       )`, cutoff).Scan(&report.FoldableSourceBytes); err != nil {
			return report, xerrors.Errorf("failed to aggregate foldable source bytes: %w", err)
		}
	}
	report.UndiscardableSourceBytes = report.RetainedSourceBytes - report.FoldableSourceBytes
	if report.UndiscardableSourceBytes < 0 {
		report.UndiscardableSourceBytes = 0
	}

	windowStart := formatTimestamp(now.UTC().AddDate(0, 0, -application.OperatorCostWindowDays))
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*), COUNT(DISTINCT session_id)
  FROM event_metadata_projection
 WHERE created_at_norm IS NOT NULL
   AND created_at_norm >= ?`, windowStart).Scan(&report.WindowEventCount, &report.WindowSessionCount); err != nil {
		return report, xerrors.Errorf("failed to aggregate trailing window activity: %w", err)
	}

	finishOperatorCost(&report)
	return report, nil
}

func finishOperatorCost(report *apptypes.OperatorCostReport) {
	report.ResidentBytesPerEvent = perUnit(report.ResidentBytes, report.EventCount)
	report.ResidentBytesPerSession = perUnit(report.ResidentBytes, report.SessionCount)
	report.UndiscardableBytesPerEvent = perUnit(report.UndiscardableSourceBytes, report.EventCount)
	report.UndiscardableBytesPerSession = perUnit(report.UndiscardableSourceBytes, report.SessionCount)
	if report.RetainedSourceBytes > 0 {
		report.Amplification = float64(report.ResidentBytes) / float64(report.RetainedSourceBytes)
	}
	days := float64(report.WindowDays)
	if days <= 0 {
		days = float64(application.OperatorCostWindowDays)
	}
	report.EventsPerDay = float64(report.WindowEventCount) / days
	report.SessionsPerDay = float64(report.WindowSessionCount) / days
	if report.WindowEventCount > 0 {
		report.ProjectedUndiscardableBytesPerMonth = int64(report.EventsPerDay * 30 * report.UndiscardableBytesPerEvent)
	}
	report.Evidence.Status = "complete"
	report.Evidence.Method = "event_metadata_projection"
}

func perUnit(total, count int64) float64 {
	if count <= 0 {
		return 0
	}
	return float64(total) / float64(count)
}
