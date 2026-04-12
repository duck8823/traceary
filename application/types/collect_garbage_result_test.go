package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestCollectGarbageResultOf_Getters(t *testing.T) {
	t.Parallel()

	before := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name         string
		deletedCount int
		before       time.Time
		dryRun       bool
	}{
		{
			name:         "dry run",
			deletedCount: 0,
			before:       before,
			dryRun:       true,
		},
		{
			name:         "actual deletion",
			deletedCount: 42,
			before:       before,
			dryRun:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := apptypes.CollectGarbageResultOf(tt.deletedCount, tt.before, tt.dryRun)

			if diff := cmp.Diff(tt.deletedCount, result.DeletedCount()); diff != "" {
				t.Errorf("DeletedCount() mismatch (-want +got):\n%s", diff)
			}
			if !result.Before().Equal(tt.before) {
				t.Errorf("Before() = %v, want %v", result.Before(), tt.before)
			}
			if diff := cmp.Diff(tt.dryRun, result.DryRun()); diff != "" {
				t.Errorf("DryRun() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
