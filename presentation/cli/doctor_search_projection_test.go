package cli

import (
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestSearchProjectionBudgetDoctorCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status apptypes.SearchProjectionControlStatus
		err    error
		want   string
	}{
		{
			name:   "complete within budget passes",
			status: apptypes.SearchProjectionControlStatus{State: "complete", IndexFamilyWithinBudget: 1},
			want:   doctorStatusPass,
		},
		{
			name:   "complete over budget warns",
			status: apptypes.SearchProjectionControlStatus{State: "complete", IndexFamilyWithinBudget: 0},
			want:   doctorStatusWarn,
		},
		{
			name:   "complete unknown verdict skips",
			status: apptypes.SearchProjectionControlStatus{State: "complete", IndexFamilyWithinBudget: -1},
			want:   doctorStatusSkip,
		},
		{
			name:   "rebuilding skips",
			status: apptypes.SearchProjectionControlStatus{State: "rebuilding", IndexFamilyWithinBudget: 0},
			want:   doctorStatusSkip,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchProjectionBudgetDoctorCheck(tt.status, tt.err)
			if got.Name != "search-projection-budget" {
				t.Fatalf("name=%q", got.Name)
			}
			if got.Status != tt.want {
				t.Fatalf("status=%q, want %q", got.Status, tt.want)
			}
			if tt.want == doctorStatusWarn && got.Hint == "" {
				t.Fatal("over-budget warning must name the operator lever")
			}
		})
	}
}
