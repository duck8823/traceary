package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestSessionIDFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid session ID", input: "session-abc", want: "session-abc"},
		{name: "trims whitespace", input: "  session-def  ", want: "session-def"},
		{name: "empty string returns error", input: "", wantErr: true},
		{name: "whitespace-only returns error", input: "   ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.SessionIDFrom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SessionIDFrom(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("SessionIDFrom(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
		})
	}
}
