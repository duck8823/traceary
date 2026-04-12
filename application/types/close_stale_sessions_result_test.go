package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestCloseStaleSessionsResultOf_Getter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int
	}{
		{name: "zero closed", count: 0},
		{name: "multiple closed", count: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := apptypes.CloseStaleSessionsResultOf(tt.count)

			if diff := cmp.Diff(tt.count, result.ClosedCount()); diff != "" {
				t.Errorf("ClosedCount() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
