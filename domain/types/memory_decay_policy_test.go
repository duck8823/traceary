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
	if !p.AllowsSource(types.MemorySourceExtracted) || !p.AllowsSource(types.MemorySourceExtractedHidden) {
		t.Fatalf("default sources missing TTL auto-extract sources: %#v", p.Sources())
	}
	if p.AllowsSource(types.MemorySourceManual) || p.AllowsSource(types.MemorySourceRememberIntent) || p.AllowsSource(types.MemorySourceCompactSummary) {
		t.Fatal("manual/remember-intent/compact-summary must not be in default decay sources")
	}
}
