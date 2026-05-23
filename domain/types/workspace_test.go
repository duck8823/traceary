package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/types"
)

func TestWorkspace_IsLocalPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		workspace types.Workspace
		want      bool
	}{
		{name: "absolute path is local", workspace: types.Workspace("/Users/duck/project"), want: true},
		{name: "windows drive slash path is local", workspace: types.Workspace("C:/Users/duck/project"), want: true},
		{name: "windows drive backslash path is local", workspace: types.Workspace(`C:\Users\duck\project`), want: true},
		{name: "windows drive relative path is not local", workspace: types.Workspace(`C:Users\duck\project`), want: false},
		{name: "git remote URL is not local", workspace: types.Workspace("github.com/duck/traceary"), want: false},
		{name: "relative path is not local", workspace: types.Workspace("project"), want: false},
		{name: "empty workspace is not local", workspace: types.Workspace(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.workspace.IsLocalPath(); got != tt.want {
				t.Errorf("IsLocalPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkspace_AncestorWorkspaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		workspace types.Workspace
		want      []types.Workspace
	}{
		{
			name:      "absolute child path returns ancestors up to root",
			workspace: types.Workspace("/Users/duck/repos/project/sub"),
			want: []types.Workspace{
				types.Workspace("/Users/duck/repos/project"),
				types.Workspace("/Users/duck/repos"),
				types.Workspace("/Users/duck"),
				types.Workspace("/Users"),
				types.Workspace("/"),
			},
		},
		{
			name:      "windows drive slash path returns ancestors up to drive root",
			workspace: types.Workspace("C:/Users/duck/repos/project/sub"),
			want: []types.Workspace{
				types.Workspace("C:/Users/duck/repos/project"),
				types.Workspace("C:/Users/duck/repos"),
				types.Workspace("C:/Users/duck"),
				types.Workspace("C:/Users"),
				types.Workspace("C:/"),
			},
		},
		{
			name:      "windows drive backslash path preserves separators",
			workspace: types.Workspace(`C:\Users\duck\repos\project\sub`),
			want: []types.Workspace{
				types.Workspace(`C:\Users\duck\repos\project`),
				types.Workspace(`C:\Users\duck\repos`),
				types.Workspace(`C:\Users\duck`),
				types.Workspace(`C:\Users`),
				types.Workspace(`C:\`),
			},
		},
		{
			name:      "windows drive root has no ancestors",
			workspace: types.Workspace("C:/"),
			want:      nil,
		},
		{
			name:      "filesystem root has no ancestors",
			workspace: types.Workspace("/"),
			want:      nil,
		},
		{
			name:      "git remote URL has no ancestors",
			workspace: types.Workspace("github.com/duck/traceary"),
			want:      nil,
		},
		{
			name:      "empty workspace has no ancestors",
			workspace: types.Workspace(""),
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.workspace.AncestorWorkspaces()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("AncestorWorkspaces() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWorkspaceFrom(t *testing.T) {
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
			got, err := types.WorkspaceFrom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("WorkspaceFrom(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("WorkspaceFrom(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
		})
	}
}
