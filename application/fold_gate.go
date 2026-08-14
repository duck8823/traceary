package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// DefaultFoldThresholdBytes matches presentation.DefaultConsolidationThresholdBytes.
const DefaultFoldThresholdBytes int64 = 64 * 1024

// DefaultFoldWakeBudgetBytes matches presentation.DefaultWakeInjectionBudgetBytes.
const DefaultFoldWakeBudgetBytes int64 = 8192

// FoldGateTargetRatio is the v0.34 row: >= 95% of sessions worth folding.
const FoldGateTargetRatio = 0.95

// FoldGateContentSampleLimit is the newest agent summaries inspected for #1874.
const FoldGateContentSampleLimit = 20

// FoldGateInspector measures refinement coverage and wake eligibility.
type FoldGateInspector interface {
	InspectFoldGate(ctx context.Context, thresholdBytes, wakeBudgetBytes int64) (apptypes.FoldGateReport, error)
}
