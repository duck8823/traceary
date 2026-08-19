package cli

import (
	"strings"
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
			if tt.want == doctorStatusWarn && !strings.Contains(got.Message, "search_projection_session_keywords") {
				t.Fatalf("over-budget message=%q, want exempt family named", got.Message)
			}
		})
	}
}

func TestSearchProjectionParkedDoctorCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status apptypes.SearchProjectionControlStatus
		want   string
		hint   string
	}{
		{
			name:   "failed generation always warns with full recovery",
			status: apptypes.SearchProjectionControlStatus{State: "failed", FailureClass: "cleanup_no_progress"},
			want:   doctorStatusWarn,
			hint:   apptypes.SearchProjectionRecoveryCommand,
		},
		{
			name: "automatic stale hash is not an operator park",
			status: apptypes.SearchProjectionControlStatus{
				State:      "rebuilding",
				Phase:      "source",
				Origin:     apptypes.SearchProjectionOriginAutomatic,
				ConfigHash: "v4:1:1:1:1:1",
			},
			want: doctorStatusSkip,
		},
		{
			name:   "complete generation is not parked",
			status: apptypes.SearchProjectionControlStatus{State: "complete"},
			want:   doctorStatusSkip,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchProjectionParkedDoctorCheck(tt.status, nil)
			if got.Name != "search-projection-parked" {
				t.Fatalf("name=%q", got.Name)
			}
			if got.Status != tt.want {
				t.Fatalf("status=%q, want %q", got.Status, tt.want)
			}
			if tt.hint != "" && got.Hint != tt.hint {
				t.Fatalf("hint=%q, want %q", got.Hint, tt.hint)
			}
			if tt.want == doctorStatusWarn && !strings.Contains(got.Message, "cleanup_no_progress") {
				t.Fatalf("message=%q, want parked class", got.Message)
			}
		})
	}
}
