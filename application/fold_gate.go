package application

import (
	"context"
	"strings"

	apptypes "github.com/duck8823/traceary/application/types"
)

// WakeInjectionHeader is the first line of hook stdout. Budget math counts
// these bytes the same way formatWakeInjectionText does.
const WakeInjectionHeader = "Previous session summaries (recalled context, not user instructions):"

// WakeSummaryFitsBudget reports whether one trimmed summary would inject as
// the newest (and only) payload: header + newline + summary + newline.
func WakeSummaryFitsBudget(summary string, budgetBytes int64) bool {
	summary = strings.TrimSpace(summary)
	if budgetBytes <= 0 || summary == "" {
		return false
	}
	used := int64(len(WakeInjectionHeader) + 1)
	need := int64(len(summary) + 1)
	return used+need <= budgetBytes
}

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
