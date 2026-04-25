package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestMemorySourceFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    types.MemorySource
		wantErr bool
	}{
		{name: "manual", input: "manual", want: types.MemorySourceManual},
		{name: "imported", input: "imported", want: types.MemorySourceImported},
		{name: "rejects empty", input: "", wantErr: true},
		{name: "rejects unknown", input: "generated", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.MemorySourceFrom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MemorySourceFrom() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("MemorySourceFrom() = %v, want %v", got, tt.want)
			}
		})
	}
}
