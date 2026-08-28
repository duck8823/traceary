package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestConsolidationDeliveryFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    types.ConsolidationDelivery
		wantErr bool
	}{
		{name: "stop_exit_2", value: "stop_exit_2", want: types.ConsolidationDeliveryStopExit2},
		{name: "additional_context", value: "additional_context", want: types.ConsolidationDeliveryAdditionalContext},
		{name: "none", value: "none", want: types.ConsolidationDeliveryNone},
		{name: "trims space", value: " stop_exit_2 ", want: types.ConsolidationDeliveryStopExit2},
		{name: "unknown", value: "stderr", wantErr: true},
		{name: "empty", value: " ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.ConsolidationDeliveryFrom(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ConsolidationDeliveryFrom() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ConsolidationDeliveryFrom() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
