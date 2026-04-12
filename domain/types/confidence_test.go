package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestConfidenceOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    types.Confidence
		wantErr bool
	}{
		{name: "low", input: "low", want: types.ConfidenceLow},
		{name: "verified", input: "verified", want: types.ConfidenceVerified},
		{name: "rejects empty", input: " ", wantErr: true},
		{name: "rejects unknown", input: "certain", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.ConfidenceOf(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ConfidenceOf() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("ConfidenceOf() = %v, want %v", got, tt.want)
			}
		})
	}
}
