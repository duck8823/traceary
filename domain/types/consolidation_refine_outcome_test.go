package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestConsolidationRefineOutcomeFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    types.ConsolidationRefineOutcome
		wantErr bool
	}{
		{name: "accepted", value: "accepted", want: types.ConsolidationRefineAccepted},
		{name: "rejected", value: "rejected", want: types.ConsolidationRefineRejected},
		{name: "unknown", value: "created", wantErr: true},
		{name: "empty", value: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.ConsolidationRefineOutcomeFrom(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ConsolidationRefineOutcomeFrom() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ConsolidationRefineOutcomeFrom() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
