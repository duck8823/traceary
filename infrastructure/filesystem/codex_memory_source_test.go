package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const sampleMemoryMD = `# Task Group: v0.7 sprint
applies_to: cwd=` + "`PROJECT_DIR`" + `

## User preferences
- Prefer bulleted commit messages when the diff touches more than two files.

## Reusable knowledge
- Traceary's hexagonal architecture keeps query services in the application layer.

## Failures and how to do differently
- Earlier attempts shelled out to git; prefer reading ` + "`.git/config`" + ` directly.

## Task 1
- This should be ignored because the section is not supported.

### rollout_summary_files
- This should also be ignored.

# Task Group: docs-only sweep
applies_to: cwd=/nonexistent/project

## User preferences
- Always update both English and Japanese docs in the same PR.
`

func TestCodexMemorySource_LoadParsesSupportedSections(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	memoryRoot := filepath.Join(dir, "memories")
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("mkdir memories: %v", err)
	}
	replaced := strings.ReplaceAll(sampleMemoryMD, "`PROJECT_DIR`", projectDir)
	if err := os.WriteFile(filepath.Join(memoryRoot, "MEMORY.md"), []byte(replaced), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	source := NewCodexMemorySource()
	candidates, warnings, err := source.Load(context.Background(), apptypes.CodexImportCriteria{
		Root: memoryRoot,
	})
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if len(candidates) != 4 {
		t.Fatalf("expected 4 candidates (3 from first task group + 1 from second), got %d (warnings=%v)", len(candidates), warnings)
	}

	facts := make([]string, 0, len(candidates))
	for _, c := range candidates {
		facts = append(facts, c.Fact)
	}
	wants := []string{
		"Prefer bulleted commit messages",
		"Traceary's hexagonal architecture",
		"prefer reading",
		"Always update both English and Japanese docs",
	}
	for _, want := range wants {
		if !containsAny(facts, want) {
			t.Fatalf("missing fact %q in %v", want, facts)
		}
	}

	if candidates[0].MemoryType != domtypes.MemoryTypePreference {
		t.Fatalf("User preferences should map to preference, got %s", candidates[0].MemoryType)
	}
	if candidates[1].MemoryType != domtypes.MemoryTypeLesson {
		t.Fatalf("Reusable knowledge should map to lesson, got %s", candidates[1].MemoryType)
	}
	if candidates[2].MemoryType != domtypes.MemoryTypeLesson {
		t.Fatalf("Failures section should map to lesson, got %s", candidates[2].MemoryType)
	}

	if candidates[0].SourcePath == "" || !strings.HasSuffix(candidates[0].SourcePath, "MEMORY.md") {
		t.Fatalf("SourcePath should point at MEMORY.md, got %q", candidates[0].SourcePath)
	}
	if len(candidates[0].EvidenceRefs) != 1 || candidates[0].EvidenceRefs[0].Kind() != domtypes.EvidenceRefKindFile {
		t.Fatalf("expected file evidence ref, got %+v", candidates[0].EvidenceRefs)
	}
	if !strings.Contains(candidates[0].EvidenceRefs[0].Value(), "#L") {
		t.Fatalf("evidence ref should include line range, got %q", candidates[0].EvidenceRefs[0].Value())
	}
}

func TestCodexMemorySource_LoadMissingRootReturnsNil(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := NewCodexMemorySource()
	candidates, warnings, err := source.Load(context.Background(), apptypes.CodexImportCriteria{
		Root: filepath.Join(dir, "missing"),
	})
	if err != nil {
		t.Fatalf("unexpected error for missing root: %v", err)
	}
	if candidates != nil || warnings != nil {
		t.Fatalf("missing root should produce empty result, got candidates=%d warnings=%v", len(candidates), warnings)
	}
}

func TestCodexMemorySource_LoadRejectsSymlinkRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	actualRoot := filepath.Join(dir, "actual-root")
	if err := os.MkdirAll(actualRoot, 0o755); err != nil {
		t.Fatalf("mkdir actual: %v", err)
	}
	linkRoot := filepath.Join(dir, "link-root")
	if err := os.Symlink(actualRoot, linkRoot); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	source := NewCodexMemorySource()
	_, _, err := source.Load(context.Background(), apptypes.CodexImportCriteria{Root: linkRoot})
	if err == nil {
		t.Fatalf("expected error for symlink root, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error should mention symlink, got %q", err.Error())
	}
}

func TestCodexMemorySource_LoadUsesWorkspaceFallbackWhenCWDMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir memories: %v", err)
	}
	content := "# Task Group: misc\n\n## User preferences\n- Use semicolons in config examples.\n"
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	source := NewCodexMemorySource()
	candidates, _, err := source.Load(context.Background(), apptypes.CodexImportCriteria{
		Root:              root,
		WorkspaceFallback: workspace,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if got := candidates[0].Scope.Key(); got != "github.com/example/repo" {
		t.Fatalf("scope key = %q, want %q", got, "github.com/example/repo")
	}
}

func TestCodexMemorySource_LoadSkipsWhenScopeUnresolvable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir memories: %v", err)
	}
	content := "# Task Group: orphan\n\n## User preferences\n- Orphan bullet without cwd or fallback.\n"
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	source := NewCodexMemorySource()
	candidates, warnings, err := source.Load(context.Background(), apptypes.CodexImportCriteria{Root: root})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no candidates, got %d", len(candidates))
	}
	if len(warnings) == 0 {
		t.Fatalf("expected at least one warning when scope cannot be resolved")
	}
}

func containsAny(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
