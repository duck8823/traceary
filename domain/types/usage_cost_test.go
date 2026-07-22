package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestUsageCost_estimateRequiresVersionAndDiffersFromProviderReported(t *testing.T) {
	t.Parallel()

	if _, err := types.EstimatedUsageCost(1250, "USD", ""); err == nil {
		t.Fatal("EstimatedUsageCost without price-table version error = nil")
	}

	estimated, err := types.EstimatedUsageCost(1250, "USD", "openai-2026-07-01")
	if err != nil {
		t.Fatalf("EstimatedUsageCost() error = %v", err)
	}
	if estimated.Origin() != types.UsageCostEstimated || estimated.PriceTableVersion() != "openai-2026-07-01" {
		t.Fatalf("estimated provenance = %q/%q", estimated.Origin(), estimated.PriceTableVersion())
	}

	provider, err := types.ProviderReportedUsageCost(1250, "USD")
	if err != nil {
		t.Fatalf("ProviderReportedUsageCost() error = %v", err)
	}
	if provider.Origin() != types.UsageCostProviderReported || provider.PriceTableVersion() != "" {
		t.Fatalf("provider provenance = %q/%q", provider.Origin(), provider.PriceTableVersion())
	}
}

func TestUsageCost_unknownAndUnavailableCarryNoAmount(t *testing.T) {
	t.Parallel()

	for name, cost := range map[string]types.UsageCost{
		"unknown":     types.UnknownUsageCost(),
		"unavailable": types.UnavailableUsageCost(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := cost.AmountMicros(); ok {
				t.Fatal("AmountMicros() unexpectedly present")
			}
		})
	}
}
