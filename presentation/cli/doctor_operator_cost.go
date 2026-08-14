package cli

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

const doctorOperatorCostTimeout = 2 * time.Second

func (c *RootCLI) inspectOperatorCost(ctx context.Context, residentBytes int64) (apptypes.OperatorCostReport, doctorCheck) {
	const name = "store-operator-cost"
	if c.operatorCostInspector == nil {
		return apptypes.OperatorCostReport{}, doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("operator cost inspector is not configured", "operator cost inspector が設定されていません"),
		}
	}
	inspectCtx, cancel := context.WithTimeout(ctx, doctorOperatorCostTimeout)
	defer cancel()
	report, err := c.operatorCostInspector.InspectOperatorCost(inspectCtx, time.Now().UTC(), residentBytes)
	if err != nil {
		return apptypes.OperatorCostReport{}, doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to measure operator store cost: %v", "operator のストアコストを計測できません: %v", err),
		}
	}
	return report, buildOperatorCostCheck(report)
}

func residentOnlyOperatorCost(residentBytes int64) apptypes.OperatorCostReport {
	return apptypes.OperatorCostReport{
		SchemaVersion: "traceary.operator_cost/v1",
		WindowDays:    30,
		ResidentBytes: residentBytes,
		Evidence: apptypes.OperatorCostEvidence{
			Status: "skipped",
			Method: "filesystem",
			Reason: "default doctor is filesystem-metadata-only for stores at or above 2 GiB",
		},
	}
}

func buildOperatorCostCheck(report apptypes.OperatorCostReport) doctorCheck {
	const name = "store-operator-cost"
	if report.Evidence.Status == "skipped" {
		return doctorCheck{
			Name:   name,
			Status: doctorStatusSkip,
			Message: localizef(
				"operator store cost is incomplete (%s): resident=%s",
				"operator のストアコストは不完全です (%s): resident=%s",
				report.Evidence.Reason,
				formatByteSize(report.ResidentBytes),
			),
			Hint: Localize(
				"Detailed per-event and rate figures need a store under 2 GiB, or a reviewed copy. This is this store's measured size, not a project-wide monthly claim.",
				"イベントあたりとレートの詳細は 2 GiB 未満のストア、または確認済みコピーが必要です。これはこのストアの実測であり、プロジェクト全体の月次主張ではありません。",
			),
		}
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusPass,
		Message: localizef(
			"this store: resident=%s (%s/event, %s/session) undiscardable=%s/event amplification=%.2fx rate=%.1f events/day projected undiscardable ~%s/month",
			"このストア: resident=%s（%s/event、%s/session）undiscardable=%s/event amplification=%.2fx rate=%.1f events/day 予測 undiscardable 約%s/月",
			formatByteSize(report.ResidentBytes),
			formatByteSize(int64(report.ResidentBytesPerEvent)),
			formatByteSize(int64(report.ResidentBytesPerSession)),
			formatByteSize(int64(report.UndiscardableBytesPerEvent)),
			report.Amplification,
			report.EventsPerDay,
			formatByteSize(report.ProjectedUndiscardableBytesPerMonth),
		),
		Hint: Localize(
			"These figures are measured from this store. They are not a global monthly bound.",
			"これらの数値はこのストアの実測です。グローバルな月次上限ではありません。",
		),
	}
}
