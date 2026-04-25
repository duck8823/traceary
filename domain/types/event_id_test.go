package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestEventIDFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid event ID", input: "evt-123", want: "evt-123"},
		{name: "trims whitespace", input: "  evt-456  ", want: "evt-456"},
		{name: "empty string returns error", input: "", wantErr: true},
		{name: "whitespace-only returns error", input: "   ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.EventIDFrom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("EventIDFrom(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("EventIDFrom(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
		})
	}
}
