package types_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/types"
)

func TestToolAccessOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host types.Client
		tool string
		want types.ToolAccess
	}{
		{name: "claude/Read is read_only", host: types.Client("claude"), tool: "Read", want: types.ToolAccessReadOnly},
		{name: "claude/NotebookRead is read_only", host: types.Client("claude"), tool: "NotebookRead", want: types.ToolAccessReadOnly},
		{name: "claude/Grep is read_only", host: types.Client("claude"), tool: "Grep", want: types.ToolAccessReadOnly},
		{name: "claude/Glob is read_only", host: types.Client("claude"), tool: "Glob", want: types.ToolAccessReadOnly},
		{name: "claude/WebFetch is read_only", host: types.Client("claude"), tool: "WebFetch", want: types.ToolAccessReadOnly},
		{name: "claude/Bash is unknown", host: types.Client("claude"), tool: "Bash", want: types.ToolAccessUnknown},
		{name: "claude/Write is unknown", host: types.Client("claude"), tool: "Write", want: types.ToolAccessUnknown},
		{name: "claude/mcp__traceary__search is unknown", host: types.Client("claude"), tool: "mcp__traceary__search", want: types.ToolAccessUnknown},
		{name: "grok/read_file is read_only", host: types.Client("grok"), tool: "read_file", want: types.ToolAccessReadOnly},
		{name: "grok/grep is read_only", host: types.Client("grok"), tool: "grep", want: types.ToolAccessReadOnly},
		{name: "grok/list_dir is read_only", host: types.Client("grok"), tool: "list_dir", want: types.ToolAccessReadOnly},
		{name: "codex/shell is unknown", host: types.Client("codex"), tool: "shell", want: types.ToolAccessUnknown},
		{name: "codex/read_file is unknown because Codex tools were unconfirmed", host: types.Client("codex"), tool: "read_file", want: types.ToolAccessUnknown},
		{name: "kimi/Read is read_only", host: types.Client("kimi"), tool: "Read", want: types.ToolAccessReadOnly},
		{name: "kimi/Grep is read_only", host: types.Client("kimi"), tool: "Grep", want: types.ToolAccessReadOnly},
		{name: "gemini/read_file is read_only (unreachable via current AfterTool matcher)", host: types.Client("gemini"), tool: "read_file", want: types.ToolAccessReadOnly},
		{name: "gemini/run_shell_command is unknown", host: types.Client("gemini"), tool: "run_shell_command", want: types.ToolAccessUnknown},
		{name: "antigravity/run_command is unknown", host: types.Client("antigravity"), tool: "run_command", want: types.ToolAccessUnknown},
		{name: "unknown host is unknown", host: types.Client("unknown-host"), tool: "Read", want: types.ToolAccessUnknown},
		{name: "empty host is unknown", host: types.Client(""), tool: "Read", want: types.ToolAccessUnknown},
		{name: "tool name is trimmed", host: types.Client("claude"), tool: "  Read  ", want: types.ToolAccessReadOnly},
		{name: "matching is case-sensitive", host: types.Client("claude"), tool: "read", want: types.ToolAccessUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := types.ToolAccessOf(tt.host, tt.tool)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("ToolAccessOf(%q, %q) mismatch (-want +got):\n%s", tt.host, tt.tool, diff)
			}
		})
	}
}

func TestReadOnlyOutputMetadataOf(t *testing.T) {
	t.Parallel()

	t.Run("bytes and sha256 over the exact output", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyOutputMetadataOf([]string{"README.md"}, "hello", 64)
		if diff := cmp.Diff(5, got.Bytes()); diff != "" {
			t.Fatalf("Bytes() mismatch (-want +got):\n%s", diff)
		}
		want := types.ReadOnlyOutputMetadataOf([]string{"README.md"}, "hello", 64)
		if diff := cmp.Diff(want.SHA256(), got.SHA256()); diff != "" {
			t.Fatalf("SHA256() mismatch (-want +got):\n%s", diff)
		}
		if got.Truncated() {
			t.Fatalf("Truncated() = true, want false")
		}
	})

	t.Run("truncated when output exceeds the cap", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyOutputMetadataOf(nil, strings.Repeat("x", 21), 20)
		if !got.Truncated() {
			t.Fatalf("Truncated() = false, want true")
		}
		if diff := cmp.Diff(21, got.Bytes()); diff != "" {
			t.Fatalf("Bytes() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("cap zero never truncates", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyOutputMetadataOf(nil, strings.Repeat("x", 100), 0)
		if got.Truncated() {
			t.Fatalf("Truncated() = true, want false")
		}
	})

	t.Run("empty output has the empty-string digest", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyOutputMetadataOf(nil, "", 64)
		empty := types.ReadOnlyOutputMetadataOf(nil, "", 64)
		if diff := cmp.Diff(empty.SHA256(), got.SHA256()); diff != "" {
			t.Fatalf("empty digest mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", got.SHA256()); diff != "" {
			t.Fatalf("empty-string SHA-256 mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(0, got.Bytes()); diff != "" {
			t.Fatalf("Bytes() mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestReadOnlyToolTargetsOf(t *testing.T) {
	t.Parallel()

	t.Run("claude Read file_path", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyToolTargetsOf(types.Client("claude"), "Read", map[string]any{"file_path": "README.md"})
		if diff := cmp.Diff([]string{"README.md"}, got); diff != "" {
			t.Fatalf("targets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("claude Grep pattern and path", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyToolTargetsOf(types.Client("claude"), "Grep", map[string]any{"pattern": "ToolAccess", "path": "domain/"})
		if diff := cmp.Diff([]string{"ToolAccess", "domain/"}, got); diff != "" {
			t.Fatalf("targets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("claude WebFetch url", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyToolTargetsOf(types.Client("claude"), "WebFetch", map[string]any{"url": "https://example.test"})
		if diff := cmp.Diff([]string{"https://example.test"}, got); diff != "" {
			t.Fatalf("targets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("grok read_file target_file", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyToolTargetsOf(types.Client("grok"), "read_file", map[string]any{"target_file": "VERSION"})
		if diff := cmp.Diff([]string{"VERSION"}, got); diff != "" {
			t.Fatalf("targets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("array paths", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyToolTargetsOf(types.Client("gemini"), "read_many_files", map[string]any{"paths": []any{"a.go", "b.go"}})
		if diff := cmp.Diff([]string{"a.go", "b.go"}, got); diff != "" {
			t.Fatalf("targets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("missing input yields no targets", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyToolTargetsOf(types.Client("claude"), "Read", nil)
		if got != nil {
			t.Fatalf("targets = %v, want nil", got)
		}
	})

	t.Run("targets are capped at eight", func(t *testing.T) {
		t.Parallel()
		paths := make([]any, 0, 9)
		for i := 0; i < 9; i++ {
			paths = append(paths, string(rune('a'+i))+".go")
		}
		got := types.ReadOnlyToolTargetsOf(types.Client("gemini"), "read_many_files", map[string]any{"paths": paths})
		if diff := cmp.Diff(8, len(got)); diff != "" {
			t.Fatalf("len(targets) mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("each target is capped at 512 bytes", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("a", 600)
		got := types.ReadOnlyToolTargetsOf(types.Client("claude"), "Read", map[string]any{"file_path": long})
		if len(got) != 1 {
			t.Fatalf("len(targets) = %d, want 1", len(got))
		}
		if len(got[0]) != 512 {
			t.Fatalf("len(target) = %d, want 512", len(got[0]))
		}
	})

	t.Run("duplicates are dropped", func(t *testing.T) {
		t.Parallel()
		got := types.ReadOnlyToolTargetsOf(types.Client("claude"), "Read", map[string]any{
			"file_path": "README.md",
			"path":      "README.md",
		})
		if diff := cmp.Diff([]string{"README.md"}, got); diff != "" {
			t.Fatalf("targets mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestEncodeReadOnlyOutputMetadata_CanonicalJSON(t *testing.T) {
	t.Parallel()

	metadata := types.ReadOnlyOutputMetadataOf([]string{"README.md"}, "hello", 64)
	got, err := types.EncodeReadOnlyOutputMetadata(metadata)
	if err != nil {
		t.Fatalf("EncodeReadOnlyOutputMetadata() error = %v", err)
	}
	want := `{"bytes":5,"capture":"metadata_only","paths":["README.md"],"sha256":"` + metadata.SHA256() + `","truncated":false}`
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("canonical JSON mismatch (-want +got):\n%s", diff)
	}

	decoded, err := types.DecodeReadOnlyOutputMetadata(got)
	if err != nil {
		t.Fatalf("DecodeReadOnlyOutputMetadata() error = %v", err)
	}
	restored, ok := decoded.Value()
	if !ok {
		t.Fatal("decoded metadata is None")
	}
	if diff := cmp.Diff(metadata.SHA256(), restored.SHA256()); diff != "" {
		t.Fatalf("round-trip SHA256 mismatch (-want +got):\n%s", diff)
	}
}
