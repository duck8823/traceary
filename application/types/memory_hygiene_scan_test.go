package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemoryHygieneScanBudgetFrom_ZeroValueUsesFiniteDefaults(t *testing.T) {
	t.Parallel()

	budget, err := apptypes.MemoryHygieneScanBudgetFrom(apptypes.MemoryHygieneScanBudgetParams{})
	if err != nil {
		t.Fatalf("MemoryHygieneScanBudgetFrom() error = %v", err)
	}
	if budget.MaxRows() < 2 {
		t.Fatalf("MaxRows() = %d, want >= 2", budget.MaxRows())
	}
	if budget.MaxScanBytes() <= 0 {
		t.Fatalf("MaxScanBytes() = %d, want > 0", budget.MaxScanBytes())
	}
	if budget.MaxResultBytes() <= 0 {
		t.Fatalf("MaxResultBytes() = %d, want > 0", budget.MaxResultBytes())
	}
	if budget.MaxComparisons() <= 0 {
		t.Fatalf("MaxComparisons() = %d, want > 0", budget.MaxComparisons())
	}
	if budget.MaxDuration() <= 0 {
		t.Fatalf("MaxDuration() = %s, want > 0", budget.MaxDuration())
	}
}

func TestMemoryHygieneScanBudgetFrom_RejectsInvalidExplicitLimits(t *testing.T) {
	t.Parallel()

	valid := apptypes.MemoryHygieneScanBudgetParams{
		MaxRows:        2,
		MaxScanBytes:   1,
		MaxResultBytes: 1,
		MaxComparisons: 1,
		MaxDuration:    time.Millisecond,
	}
	tests := map[string]func(*apptypes.MemoryHygieneScanBudgetParams){
		"rows":         func(params *apptypes.MemoryHygieneScanBudgetParams) { params.MaxRows = 1 },
		"scan bytes":   func(params *apptypes.MemoryHygieneScanBudgetParams) { params.MaxScanBytes = 0 },
		"result bytes": func(params *apptypes.MemoryHygieneScanBudgetParams) { params.MaxResultBytes = 0 },
		"comparisons":  func(params *apptypes.MemoryHygieneScanBudgetParams) { params.MaxComparisons = 0 },
		"duration":     func(params *apptypes.MemoryHygieneScanBudgetParams) { params.MaxDuration = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			params := valid
			mutate(&params)
			if _, err := apptypes.MemoryHygieneScanBudgetFrom(params); err == nil {
				t.Fatal("MemoryHygieneScanBudgetFrom() error = nil, want validation error")
			}
		})
	}
}

func TestMemoryHygieneScanBudgetFrom_PreservesExplicitLimits(t *testing.T) {
	t.Parallel()

	params := apptypes.MemoryHygieneScanBudgetParams{
		MaxRows:        17,
		MaxScanBytes:   1_024,
		MaxResultBytes: 512,
		MaxComparisons: 19,
		MaxDuration:    125 * time.Millisecond,
	}
	budget, err := apptypes.MemoryHygieneScanBudgetFrom(params)
	if err != nil {
		t.Fatalf("MemoryHygieneScanBudgetFrom() error = %v", err)
	}
	if budget.MaxRows() != params.MaxRows ||
		budget.MaxScanBytes() != params.MaxScanBytes ||
		budget.MaxResultBytes() != params.MaxResultBytes ||
		budget.MaxComparisons() != params.MaxComparisons ||
		budget.MaxDuration() != params.MaxDuration {
		t.Fatalf("resolved budget = (%d,%d,%d,%d,%s), want explicit params",
			budget.MaxRows(),
			budget.MaxScanBytes(),
			budget.MaxResultBytes(),
			budget.MaxComparisons(),
			budget.MaxDuration(),
		)
	}
}

func TestHygieneCandidateNoiseSources_HiddenIsOptIn(t *testing.T) {
	t.Parallel()

	wantVisible := []domtypes.MemorySource{
		domtypes.MemorySourceExtracted,
		domtypes.MemorySourceRememberIntent,
		domtypes.MemorySourceCompactSummary,
	}
	if diff := cmp.Diff(wantVisible, apptypes.HygieneCandidateNoiseSources(false)); diff != "" {
		t.Fatalf("HygieneCandidateNoiseSources(false) mismatch (-want +got):\n%s", diff)
	}
	wantHidden := append(append([]domtypes.MemorySource(nil), wantVisible...), domtypes.MemorySourceExtractedHidden)
	if diff := cmp.Diff(wantHidden, apptypes.HygieneCandidateNoiseSources(true)); diff != "" {
		t.Fatalf("HygieneCandidateNoiseSources(true) mismatch (-want +got):\n%s", diff)
	}

	cases := []struct {
		source domtypes.MemorySource
		hidden bool
		want   bool
	}{
		{source: domtypes.MemorySourceExtracted, hidden: false, want: true},
		{source: domtypes.MemorySourceRememberIntent, hidden: false, want: true},
		{source: domtypes.MemorySourceCompactSummary, hidden: false, want: true},
		{source: domtypes.MemorySourceExtractedHidden, hidden: false, want: false},
		{source: domtypes.MemorySourceExtractedHidden, hidden: true, want: true},
		{source: domtypes.MemorySourceManual, hidden: true, want: false},
	}
	for _, tc := range cases {
		if got := apptypes.IsHygieneCandidateNoiseSource(tc.source, tc.hidden); got != tc.want {
			t.Fatalf("IsHygieneCandidateNoiseSource(%q, includeHidden=%v) = %v, want %v",
				tc.source, tc.hidden, got, tc.want)
		}
	}
}
