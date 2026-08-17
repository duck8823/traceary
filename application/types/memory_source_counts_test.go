package types_test

import (
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestMemorySourceCountsFromIgnoresNonPositiveBuckets(t *testing.T) {
	t.Parallel()

	counts := apptypes.MemorySourceCountsFrom(map[types.MemorySource]int{
		types.MemorySourceExtracted:       3,
		types.MemorySourceExtractedHidden: 0,
		types.MemorySourceRememberIntent:  -1,
		types.MemorySourceManual:          2,
	})
	if counts.Total() != 5 {
		t.Fatalf("Total() = %d, want 5", counts.Total())
	}
	if counts.Count(types.MemorySourceExtracted) != 3 {
		t.Fatalf("extracted = %d, want 3", counts.Count(types.MemorySourceExtracted))
	}
	if counts.Count(types.MemorySourceExtractedHidden) != 0 {
		t.Fatalf("hidden zero bucket leaked: %d", counts.Count(types.MemorySourceExtractedHidden))
	}
	if counts.Count(types.MemorySourceImported) != 0 {
		t.Fatalf("missing source must be 0, got %d", counts.Count(types.MemorySourceImported))
	}
}
