//go:build !unix

package filesystem

import (
	"context"
	"strings"
	"testing"
)

func TestKimiUsageSourceLoadFailsClosedWhenPlatformIsUnsupported(t *testing.T) {
	t.Parallel()

	result, err := NewKimiUsageSource().Load(context.Background(), "provider-session")
	if err == nil || !strings.Contains(err.Error(), "requires Unix") {
		t.Fatalf("Load() error = %v, want unsupported-platform failure", err)
	}
	if len(result.Samples) != 0 || result.LatestTurnOrdinal != 0 {
		t.Fatalf("result = %+v, want no fabricated usage data", result)
	}
}
