package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestWorkspaceOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "github repo path", input: "github.com/duck8823/traceary", want: "github.com/duck8823/traceary"},
		{name: "absolute path", input: "/home/user/project", want: "/home/user/project"},
		{name: "trims whitespace", input: "  github.com/org/repo  ", want: "github.com/org/repo"},
		{name: "accepts any non-empty string", input: "my-workspace", want: "my-workspace"},
		{name: "empty string returns error", input: "", wantErr: true},
		{name: "whitespace-only returns error", input: "   ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.WorkspaceOf(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("WorkspaceOf(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("WorkspaceOf(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
		})
	}
}
