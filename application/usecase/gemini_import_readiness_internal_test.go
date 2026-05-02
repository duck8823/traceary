package usecase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

// TestGeminiActivationTarget_ImportLineMatchesDocumentedGeminiFormat is
// the readiness-gate evidence required by the v0.13 host-native memory
// activation ADR for Gemini. Live-launching Gemini CLI from CI is
// non-deterministic (auth state, model availability, hierarchical
// memory file load order), so this test instead pins the rendered
// import line to the exact markdown form Gemini's official Memory
// Import Processor documentation says it expands at session start:
//
//	@<relative-path-to-import>
//
// Source: <https://google-gemini.github.io/gemini-cli/docs/core/memport.html>.
// The accompanying scripts/smoke_test_gemini_activation.sh materialises
// the same plan onto disk so an operator can run `gemini` in a temp
// project and confirm the live runtime resolution. The two together —
// this test plus that script — form the documented readiness evidence
// the ADR requires before a Gemini --apply PR (#895) can graduate to
// ready.
func TestGeminiActivationTarget_ImportLineMatchesDocumentedGeminiFormat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resolution, err := geminiActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetGemini,
		Root:   root,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetGemini, resolution.ImportPath)

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
		t.Fatalf("import line must start with '@' per Gemini memory import docs, got %q", importLine)
	}
	importPath := strings.TrimPrefix(importLine, "@")

	// Gemini's Memory Import Processor resolves relative imports
	// against the directory containing the host context file. Render
	// the relative path back to an absolute one and confirm it points
	// at the planned external memory file the activation usecase will
	// manage.
	hostDir := filepath.Dir(resolution.HostContextPath)
	resolvedAbs := filepath.Clean(filepath.Join(hostDir, filepath.FromSlash(importPath)))
	if resolvedAbs != resolution.ExternalMemoryPath {
		t.Fatalf("import line %q does not resolve to the planned external memory file: got %q, want %q", importLine, resolvedAbs, resolution.ExternalMemoryPath)
	}

	// The default v0.13 layout must use the documented hidden
	// directory import. Pin the canonical relative form so a future
	// refactor cannot silently switch to an absolute or escaping path.
	if importPath != "./.traceary/memories/gemini.md" {
		t.Fatalf("default import path = %q, want ./.traceary/memories/gemini.md", importPath)
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

	// The DO NOT EDIT warning must reference the gemini target so the
	// remediation command points users at the correct activate
	// invocation. Renderers that branch on target wording would
	// otherwise leave Gemini operators with a Claude-pointing message.
	warning := lines[1]
	if !strings.Contains(warning, "--target gemini") {
		t.Fatalf("stub warning must mention --target gemini, got %q", warning)
	}

	// Sanity-check that the internal planner can materialise the pair
	// onto the real filesystem under the same temp root used by the
	// smoke script. The product-level `memory activate --target gemini
	// --apply` command remains refused until #895; this internal
	// writeback is readiness evidence that the shared safe writer can
	// already handle the Gemini layout once the public apply gate is
	// opened.
	planner := &importStubActivationPlanner{}
	plan := planner.Plan(importStubActivationCriteria{
		Target:             apptypes.MemoryBridgeTargetGemini,
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

	applyResult, err := planner.Apply(plan)
	if err != nil {
		t.Fatalf("Apply internal planner: %v", err)
	}
	if applyResult.HostContext.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("applied HostContext.Status = %q, want in_sync", applyResult.HostContext.Status)
	}
	if applyResult.ExternalMemory.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("applied ExternalMemory.Status = %q, want in_sync", applyResult.ExternalMemory.Status)
	}
	roundtrip, err := os.ReadFile(resolution.HostContextPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(roundtrip), importLine) {
		t.Fatalf("materialised GEMINI.md missing import line %q, got %q", importLine, roundtrip)
	}
}
