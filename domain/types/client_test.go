package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestClientOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid client", input: "cli", want: "cli"},
		{name: "hook client", input: "hook", want: "hook"},
		{name: "mcp client", input: "mcp", want: "mcp"},
		{name: "trims whitespace", input: "  cli  ", want: "cli"},
		{name: "empty string returns error", input: "", wantErr: true},
		{name: "whitespace-only returns error", input: "   ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.ClientOf(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ClientOf(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("ClientOf(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
		})
	}
}
