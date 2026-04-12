package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestMemoryTypeOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    types.MemoryType
		wantErr bool
	}{
		{name: "preference", input: "preference", want: types.MemoryTypePreference},
		{name: "artifact", input: "artifact", want: types.MemoryTypeArtifact},
		{name: "rejects empty", input: " ", wantErr: true},
		{name: "rejects unknown", input: "unknown", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.MemoryTypeOf(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MemoryTypeOf() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("MemoryTypeOf() = %v, want %v", got, tt.want)
			}
		})
	}
}
