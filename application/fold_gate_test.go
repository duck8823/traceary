package application_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
)

func TestWakeSummaryFitsBudgetCountsHeaderAndNewlines(t *testing.T) {
	t.Parallel()
	budget := application.DefaultFoldWakeBudgetBytes
	headerOverhead := int64(len(application.WakeInjectionHeader) + 2)
	if application.WakeSummaryFitsBudget(strings.Repeat("x", int(budget)), budget) {
		t.Fatal("raw-budget-length summary must not fit once the header is counted")
	}
	if !application.WakeSummaryFitsBudget(strings.Repeat("x", int(budget-headerOverhead)), budget) {
		t.Fatal("summary that fills the remaining budget after the header must fit")
	}
	if application.WakeSummaryFitsBudget("   ", budget) {
		t.Fatal("whitespace-only summary must not fit")
	}
}
