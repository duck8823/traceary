package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestBodyAvailabilityFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		available bool
		wantErr   bool
	}{
		{name: "available", value: "available", available: true},
		{name: "unavailable by retention", value: "unavailable_retention"},
		{name: "unknown", value: "missing", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := types.BodyAvailabilityFrom(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("BodyAvailabilityFrom() error = %v, wantErr %t", err, tt.wantErr)
			}
			if err == nil && got.IsAvailable() != tt.available {
				t.Fatalf("IsAvailable() = %t, want %t", got.IsAvailable(), tt.available)
			}
		})
	}
}
