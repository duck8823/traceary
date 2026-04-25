package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestAgentFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid agent", input: "claude", want: "claude"},
		{name: "trims whitespace", input: "  codex  ", want: "codex"},
		{name: "hierarchical agent", input: "claude/Explore", want: "claude/Explore"},
		{name: "empty string returns error", input: "", wantErr: true},
		{name: "whitespace-only returns error", input: "   ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.AgentFrom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("AgentFrom(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("AgentFrom(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
		})
	}
}
