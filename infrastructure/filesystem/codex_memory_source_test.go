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

func TestCodexMemorySource_LoadParsesMultiFileLayout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir memories: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "confluence-writing.md"), []byte(`# Confluence writing
- Prefer short sections with explicit owners.
* Link related Jira tickets from the summary.
`), 0o600); err != nil {
		t.Fatalf("write confluence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pr-title-format.md"), []byte(`# PR title format
1. Prefix release issues with the version.
2. Keep the title imperative.
`), 0o600); err != nil {
		t.Fatalf("write pr-title: %v", err)
	}
	nested := filepath.Join(root, "teams")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "jira-ticket-creation-rules.md"), []byte(`## Ticket rules
- Include acceptance criteria.
`), 0o600); err != nil {
		t.Fatalf("write nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "raw_memories.txt"), []byte("- ignored\n"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	source := NewCodexMemorySource()
	candidates, warnings, err := source.Load(context.Background(), apptypes.CodexImportCriteria{
		Root:              root,
		WorkspaceFallback: workspace,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(candidates) != 5 {
		t.Fatalf("expected 5 candidates from markdown shards, got %d", len(candidates))
	}

	gotFacts := make([]string, 0, len(candidates))
	gotPaths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		gotFacts = append(gotFacts, candidate.Fact)
		gotPaths = append(gotPaths, filepath.Base(candidate.SourcePath))
		if len(candidate.EvidenceRefs) != 1 || !strings.Contains(candidate.EvidenceRefs[0].Value(), "#L") {
			t.Fatalf("candidate %q missing line-range evidence: %+v", candidate.Fact, candidate.EvidenceRefs)
		}
		if len(candidate.ArtifactRefs) != 1 || candidate.ArtifactRefs[0].Value() != candidate.SourcePath {
			t.Fatalf("candidate %q missing source artifact: %+v", candidate.Fact, candidate.ArtifactRefs)
		}
		if candidate.Scope.Key() != "github.com/example/repo" {
			t.Fatalf("candidate %q scope = %q, want workspace fallback", candidate.Fact, candidate.Scope.Key())
		}
	}
	wantFacts := []string{
		"Prefer short sections with explicit owners.",
		"Link related Jira tickets from the summary.",
		"Prefix release issues with the version.",
		"Keep the title imperative.",
		"Include acceptance criteria.",
	}
	for _, want := range wantFacts {
		if !containsAny(gotFacts, want) {
			t.Fatalf("missing fact %q in %v", want, gotFacts)
		}
	}
	if gotPaths[0] != "confluence-writing.md" || gotPaths[2] != "pr-title-format.md" || gotPaths[4] != "jira-ticket-creation-rules.md" {
		t.Fatalf("import order should be deterministic by path, got paths %v", gotPaths)
	}
	if candidates[2].MemoryType != domtypes.MemoryTypeConstraint {
		t.Fatalf("pr-title-format.md should infer constraint memories, got %s", candidates[2].MemoryType)
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

func TestCodexMemorySource_LoadAcceptsSymlinkedRoot(t *testing.T) {
	t.Parallel()

	// Dotfile setups commonly symlink ~/.codex at a real directory, so the
	// source adapter must follow the root symlink and still enforce path
	// containment against the resolved target rather than rejecting the
	// symlink outright.
	dir := t.TempDir()
	actualRoot := filepath.Join(dir, "actual-root")
	if err := os.MkdirAll(actualRoot, 0o755); err != nil {
		t.Fatalf("mkdir actual: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(actualRoot, "MEMORY.md"),
		[]byte("# Task Group: x\napplies_to: cwd=/abs/path\n\n## User preferences\n- accepted bullet via symlinked root.\n"),
		0o600,
	); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	linkRoot := filepath.Join(dir, "link-root")
	if err := os.Symlink(actualRoot, linkRoot); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	source := NewCodexMemorySource()
	candidates, _, err := source.Load(context.Background(), apptypes.CodexImportCriteria{Root: linkRoot})
	if err != nil {
		t.Fatalf("Load for symlinked root should succeed, got %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate via symlinked root, got %d", len(candidates))
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

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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

func TestCodexMemorySource_LoadRejectsSymlinkedMemoryFile(t *testing.T) {
	t.Parallel()

	// MEMORY.md must stay the single handbook the import reader touches.
	// If a symlink could redirect it (for example at raw_memories.md), the
	// "handbook only" scope guarantee breaks, so Load rejects a symlinked
	// entry even when the resolved target is still inside the root.
	dir := t.TempDir()
	root := filepath.Join(dir, "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir memories: %v", err)
	}
	target := filepath.Join(root, "raw_memories.md")
	if err := os.WriteFile(target, []byte("- not a handbook bullet\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(root, "MEMORY.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	source := NewCodexMemorySource()
	_, _, err := source.Load(context.Background(), apptypes.CodexImportCriteria{Root: root})
	if err == nil {
		t.Fatalf("expected error when MEMORY.md is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error should mention symlink, got %q", err.Error())
	}
}

func TestCodexMemorySource_LoadEmitsWarningForOversizedLine(t *testing.T) {
	t.Parallel()

	// A single pathological line must not kill the whole import. The parser
	// now emits a warning and keeps the otherwise valid bullets usable so
	// --watch does not terminate when a malformed handbook shows up.
	dir := t.TempDir()
	root := filepath.Join(dir, "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir memories: %v", err)
	}
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	giant := strings.Repeat("a", 64*1024)
	content := "# Task Group: x\n\n## User preferences\n- short bullet before giant.\n- " + giant + "\n- short bullet after giant.\n"
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	source := NewCodexMemorySource()
	candidates, warnings, err := source.Load(context.Background(), apptypes.CodexImportCriteria{
		Root:              root,
		WorkspaceFallback: workspace,
	})
	if err != nil {
		t.Fatalf("Load should not fail on oversized bullet, got %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 surviving candidates, got %d (warnings=%v)", len(candidates), warnings)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a size-guard warning, got none")
	}
}

func TestCodexMemorySource_LoadSkipsOversizedMarkdownFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir memories: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(strings.Repeat("x", 128)), 0o600); err != nil {
		t.Fatalf("write large: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "small.md"), []byte("- usable bullet\n"), 0o600); err != nil {
		t.Fatalf("write small: %v", err)
	}
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}

	source := &codexMemorySource{maxBulletBytes: defaultCodexMaxBulletBytes, maxFileBytes: 32}
	candidates, warnings, err := source.Load(context.Background(), apptypes.CodexImportCriteria{
		Root:              root,
		WorkspaceFallback: workspace,
	})
	if err != nil {
		t.Fatalf("Load should not fail on oversized file, got %v", err)
	}
	if len(candidates) != 1 || candidates[0].Fact != "usable bullet" {
		t.Fatalf("expected only small.md candidate, got %+v", candidates)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "exceeds size guard") {
		t.Fatalf("expected oversized-file warning, got %v", warnings)
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
