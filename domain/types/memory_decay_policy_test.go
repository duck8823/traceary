package types_test

import (
	"testing"
	"time"

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

func TestMemoryDecayPolicy_CandidateDueHonorsShorterOlderThanAndRestore(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	sevenDays, err := types.MemoryDecayPolicyOf(7*24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	originalStamp := types.Some(created.Add(types.DefaultMemoryDecayOlderThan))
	if !sevenDays.CandidateDue(created, originalStamp, now) {
		t.Fatal("original 30d stamp must still be due under --older-than 7d after 10 days")
	}
	fresh := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	if sevenDays.CandidateDue(fresh, types.Some(fresh.Add(types.DefaultMemoryDecayOlderThan)), now) {
		t.Fatal("brand-new stamped candidate must not be due at create time")
	}
	restoreAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	restoredStamp := types.Some(restoreAt.Add(types.DefaultMemoryDecayOlderThan))
	if sevenDays.CandidateDue(created, restoredStamp, restoreAt) {
		t.Fatal("just-restored candidate must not be immediately due")
	}
	if !sevenDays.CandidateDue(created, restoredStamp, restoreAt.Add(8*24*time.Hour)) {
		t.Fatal("restored candidate must be due after the shorter --older-than window")
	}
}
