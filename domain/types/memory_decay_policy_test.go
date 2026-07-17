package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestMemoryDecayPolicyOf_RejectsNonPositive(t *testing.T) {
	t.Parallel()
	if _, err := types.MemoryDecayPolicyOf(0, nil); err == nil {
		t.Fatal("expected error for zero olderThan")
	}
}

func TestMemoryDecayPolicyOf_DefaultsSources(t *testing.T) {
	t.Parallel()
	p, err := types.MemoryDecayPolicyOf(types.DefaultMemoryDecayOlderThan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.AllowsSource(types.MemorySourceExtracted) || !p.AllowsSource(types.MemorySourceCompactSummary) {
		t.Fatalf("default sources missing auto-extract sources: %#v", p.Sources())
	}
	if p.AllowsSource(types.MemorySourceManual) || p.AllowsSource(types.MemorySourceRememberIntent) {
		t.Fatal("manual/remember-intent must not be in default decay sources")
	}
}
