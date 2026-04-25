package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestMemoryStatusFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    types.MemoryStatus
		wantErr bool
	}{
		{name: "candidate", input: "candidate", want: types.MemoryStatusCandidate},
		{name: "expired", input: "expired", want: types.MemoryStatusExpired},
		{name: "rejects empty", input: "", wantErr: true},
		{name: "rejects unknown", input: "future", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.MemoryStatusFrom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MemoryStatusFrom() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("MemoryStatusFrom() = %v, want %v", got, tt.want)
			}
		})
	}
}
