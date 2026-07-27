package mcpserver

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestMemoryHygieneBudgetFromInput_UsesFiniteDefaultsAndOverridesIndividually(t *testing.T) {
	t.Parallel()

	defaults, err := memoryHygieneBudgetFromInput(scanMemoryHygieneInput{})
	if err != nil {
		t.Fatalf("memoryHygieneBudgetFromInput(defaults) error = %v", err)
	}
	if defaults.MaxRows() < 2 ||
		defaults.MaxScanBytes() <= 0 ||
		defaults.MaxResultBytes() <= 0 ||
		defaults.MaxComparisons() <= 0 ||
		defaults.MaxDuration() <= 0 {
		t.Fatalf("defaults are not finite: rows=%d scan=%d result=%d comparisons=%d duration=%s",
			defaults.MaxRows(),
			defaults.MaxScanBytes(),
			defaults.MaxResultBytes(),
			defaults.MaxComparisons(),
			defaults.MaxDuration(),
		)
	}

	overridden, err := memoryHygieneBudgetFromInput(scanMemoryHygieneInput{
		MaxScanRows:       17,
		MaxDurationMillis: 250,
	})
	if err != nil {
		t.Fatalf("memoryHygieneBudgetFromInput(overrides) error = %v", err)
	}
	if overridden.MaxRows() != 17 || overridden.MaxDuration() != 250*time.Millisecond {
		t.Fatalf("overrides = rows:%d duration:%s, want 17/250ms", overridden.MaxRows(), overridden.MaxDuration())
	}
	if overridden.MaxScanBytes() != apptypes.DefaultMemoryHygieneScanBudget().MaxScanBytes() {
		t.Fatalf("unspecified scan-byte limit changed to %d", overridden.MaxScanBytes())
	}
}

func TestMemoryHygieneBudgetFromInput_RejectsInvalidAndOverflowedValues(t *testing.T) {
	t.Parallel()

	tests := map[string]scanMemoryHygieneInput{
		"row minimum":       {MaxScanRows: 1},
		"negative bytes":    {MaxScanBytes: -1},
		"duration overflow": {MaxDurationMillis: int64((1<<63)-1)/int64(time.Millisecond) + 1},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := memoryHygieneBudgetFromInput(input); err == nil {
				t.Fatal("memoryHygieneBudgetFromInput() error = nil, want validation error")
			}
		})
	}
}
