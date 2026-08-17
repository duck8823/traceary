package types

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDefaultSearchProjectionBudgetHashPin(t *testing.T) {
	t.Parallel()

	// If this fails, a default identity field or the v4 formula changed.
	// Do not bump SearchProjectionCapacitySemanticsVersion just so CatchUp
	// replaces automatic generations: #1752 would also replace operator-owned
	// --index-family-bytes generations. Automatic stale-default recovery is
	// provenance (#1861). Bump the version only when the capacity model itself
	// changes (what a generation contains / exclusion rules).
	const wantHash = "v4:8388608:8388608:8388608:2592000000000000:1535115264"
	if got := DefaultSearchProjectionBudget().ConfigHash(); got != wantHash {
		t.Fatalf("DefaultSearchProjectionBudget().ConfigHash() = %q, want %q", got, wantHash)
	}
	if SearchProjectionCapacitySemanticsVersion != 2 {
		t.Fatalf("SearchProjectionCapacitySemanticsVersion = %d, want 2 (see #1861 pin comment)", SearchProjectionCapacitySemanticsVersion)
	}
}

func TestSearchProjectionStatusApplyParkedNotice(t *testing.T) {
	t.Parallel()

	defaultHash := DefaultSearchProjectionBudget().ConfigHash()
	tests := []struct {
		name   string
		status SearchProjectionStatus
		want   SearchProjectionStatus
	}{
		{
			name: "failed names the class and the start command",
			status: SearchProjectionStatus{
				State:        "failed",
				FailureClass: "decoded_bytes",
			},
			want: SearchProjectionStatus{
				State:           "failed",
				FailureClass:    "decoded_bytes",
				ParkedReason:    "parked after generation failure decoded_bytes",
				RecoveryCommand: SearchProjectionRecoveryCommand,
			},
		},
		{
			name: "operator hash mismatch is parked",
			status: SearchProjectionStatus{
				State:      "rebuilding",
				Phase:      "source",
				Origin:     SearchProjectionOriginOperator,
				ConfigHash: "v4:1:1:1:1:1",
			},
			want: SearchProjectionStatus{
				State:           "rebuilding",
				Phase:           "source",
				Origin:          SearchProjectionOriginOperator,
				ConfigHash:      "v4:1:1:1:1:1",
				ParkedReason:    "budget does not match generation configuration",
				RecoveryCommand: SearchProjectionRecoveryCommand,
			},
		},
		{
			name: "automatic stale hash tells the operator the next open replaces it",
			status: SearchProjectionStatus{
				State:      "rebuilding",
				Phase:      "source",
				Origin:     SearchProjectionOriginAutomatic,
				ConfigHash: "v4:1:1:1:1:1",
			},
			want: SearchProjectionStatus{
				State:        "rebuilding",
				Phase:        "source",
				Origin:       SearchProjectionOriginAutomatic,
				ConfigHash:   "v4:1:1:1:1:1",
				ParkedReason: "automatic generation budget is stale; the next store open replaces this generation",
			},
		},
		{
			name: "matching hash is not parked",
			status: SearchProjectionStatus{
				State:      "rebuilding",
				Phase:      "source",
				Origin:     SearchProjectionOriginAutomatic,
				ConfigHash: defaultHash,
			},
			want: SearchProjectionStatus{
				State:      "rebuilding",
				Phase:      "source",
				Origin:     SearchProjectionOriginAutomatic,
				ConfigHash: defaultHash,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.status
			got.ApplyParkedNotice(defaultHash)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("ApplyParkedNotice() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
