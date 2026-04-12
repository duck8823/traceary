package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestMemoryIDOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "accepts non-empty", input: "mem-1", want: "mem-1"},
		{name: "trims whitespace", input: "  mem-2  ", want: "mem-2"},
		{name: "rejects empty", input: " ", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.MemoryIDOf(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MemoryIDOf() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Fatalf("MemoryIDOf().String() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}
