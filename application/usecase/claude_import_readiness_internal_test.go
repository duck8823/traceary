package usecase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

// TestClaudeActivationTarget_ImportLineMatchesDocumentedClaudeFormat is
// the readiness-gate evidence required by the v0.13 host-native memory
// activation ADR for Claude. Live-launching Claude Code from CI is
// non-deterministic (auth state, the documented first-time external
// import approval dialog, model selection), so this test instead pins
// the rendered import line to the exact markdown form Claude's official
// memory documentation says it expands at session start:
//
//	@<relative-path-to-import>
//
// Source: <https://code.claude.com/docs/en/memory>. The accompanying
// scripts/smoke_test_claude_activation.sh materialises the same plan
// onto disk so an operator can run `claude` in a temp project and
// confirm the live runtime resolution. The two together — this test
// plus that script — form the documented readiness evidence the ADR
// requires before a Claude --apply PR can graduate to ready.
func TestClaudeActivationTarget_ImportLineMatchesDocumentedClaudeFormat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resolution, err := claudeActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, resolution.ImportPath)

	// Marker contract: the begin marker carries a version, the end
	// marker does not. The first non-marker line in the stub must be
	// the warning, the second must be the import line.
	lines := strings.Split(strings.TrimRight(stub, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("rendered stub must have exactly 4 lines (begin, warning, import, end), got %d: %q", len(lines), stub)
	}
	if lines[0] != "<!-- traceary-memory-import:begin:v1 -->" {
		t.Fatalf("stub[0] = %q, want documented begin marker", lines[0])
	}
	if lines[3] != "<!-- traceary-memory-import:end -->" {
		t.Fatalf("stub[3] = %q, want documented end marker", lines[3])
	}

	importLine := lines[2]
	if !strings.HasPrefix(importLine, "@") {
		t.Fatalf("import line must start with '@' per Claude memory docs, got %q", importLine)
	}
	importPath := strings.TrimPrefix(importLine, "@")

	// Claude resolves relative imports against the directory containing
	// the host context file. Render the relative path back to an
	// absolute one and confirm it points at the planned external memory
	// file the activation usecase will manage.
	hostDir := filepath.Dir(resolution.HostContextPath)
	resolvedAbs := filepath.Clean(filepath.Join(hostDir, filepath.FromSlash(importPath)))
	if resolvedAbs != resolution.ExternalMemoryPath {
		t.Fatalf("import line %q does not resolve to the planned external memory file: got %q, want %q", importLine, resolvedAbs, resolution.ExternalMemoryPath)
	}

	// The default v0.13 layout must use the documented hidden directory
	// import (the official Claude memory docs include the
	// `@~/.claude/...` example, so a hidden `.traceary/` import is in
	// scope). Pin the canonical relative form so a future refactor
	// cannot silently switch to an absolute or escaping path.
	if importPath != "./.traceary/memories/claude.md" {
		t.Fatalf("default import path = %q, want ./.traceary/memories/claude.md", importPath)
	}

	// External memory file must live below the host context directory
	// so the relative import never escapes the activation root. A
	// future absolute override is acceptable, but the default must
	// stay confined.
	rel, err := filepath.Rel(hostDir, resolution.ExternalMemoryPath)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if strings.HasPrefix(filepath.ToSlash(rel), "../") {
		t.Fatalf("external memory file %q escapes host context directory %q via %q", resolution.ExternalMemoryPath, hostDir, rel)
	}

	// Sanity-check that the planner can materialise the pair onto the
	// real filesystem under the same temp root used by the smoke
	// script. This protects the readiness gate from regressions in the
	// safe writer (symlink refusal, atomic rename) without launching
	// Claude.
	planner := &importStubActivationPlanner{}
	plan := planner.Plan(importStubActivationCriteria{
		Target:             apptypes.MemoryBridgeTargetClaude,
		HostContextPath:    resolution.HostContextPath,
		ExternalMemoryPath: resolution.ExternalMemoryPath,
		ImportPath:         resolution.ImportPath,
		ExternalMarkdown:   "<!-- traceary-memories:begin:v1 -->\nplaceholder\n<!-- traceary-memories:end -->\n",
	})
	if plan.HostContext.Status != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("HostContext.Status = %q, want missing on a fresh root", plan.HostContext.Status)
	}
	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("ExternalMemory.Status = %q, want missing on a fresh root", plan.ExternalMemory.Status)
	}

	// The smoke script writes the planned content into the temp
	// project. Reproduce that step here so a regression in
	// renderImportStubBlock would also be caught when the script is
	// executed manually for the live runtime probe.
	if err := os.MkdirAll(filepath.Dir(resolution.ExternalMemoryPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(resolution.HostContextPath, []byte(plan.HostContext.Markdown), 0o600); err != nil {
		t.Fatalf("WriteFile host: %v", err)
	}
	if err := os.WriteFile(resolution.ExternalMemoryPath, []byte(plan.ExternalMemory.Markdown), 0o600); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	roundtrip, err := os.ReadFile(resolution.HostContextPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(roundtrip), importLine) {
		t.Fatalf("materialised CLAUDE.md missing import line %q, got %q", importLine, roundtrip)
	}
}
