package usecase

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestRenderImportStubBlock_RendersCanonicalLinesForClaude(t *testing.T) {
	t.Parallel()

	got := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	want := strings.Join([]string{
		"<!-- traceary-memory-import:begin:v1 -->",
		"<!-- DO NOT EDIT: this import is managed by Traceary. Run `traceary memory activate --target claude --dry-run --diff` before applying updates. -->",
		"@./.traceary/memories/claude.md",
		"<!-- traceary-memory-import:end -->",
		"",
	}, "\n")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("renderImportStubBlock mismatch (-want +got):\n%s", diff)
	}
}

func TestRenderImportStubBlock_TargetNameAppearsInWarningForGemini(t *testing.T) {
	t.Parallel()

	got := renderImportStubBlock(apptypes.MemoryBridgeTargetGemini, "/abs/path/external.md")
	if !strings.Contains(got, "--target gemini") {
		t.Fatalf("warning missing target name: %q", got)
	}
	if !strings.Contains(got, "@/abs/path/external.md") {
		t.Fatalf("absolute import path missing from stub: %q", got)
	}
}

func TestImportStubBlockMarkers_FindsCanonicalStubRegion(t *testing.T) {
	t.Parallel()

	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	content := "# CLAUDE.md\n\n" + stub + "\n## user section\n"
	region, found, err := importStubBlockMarkers.findRegion(content)
	if err != nil {
		t.Fatalf("findRegion error = %v", err)
	}
	if !found {
		t.Fatalf("findRegion found = false, want true")
	}
	if got := content[region.start:region.end]; got != stub {
		t.Fatalf("region content mismatch: got %q want %q", got, stub)
	}
}

func TestImportStubBlockMarkers_RejectsNewerStubVersion(t *testing.T) {
	t.Parallel()

	content := "<!-- traceary-memory-import:begin:v9 -->\n@./.traceary/memories/claude.md\n<!-- traceary-memory-import:end -->\n"
	_, _, err := importStubBlockMarkers.findRegion(content)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite newer Traceary managed block version v9") {
		t.Fatalf("findRegion err = %v, want newer-version rejection", err)
	}
}

func TestImportStubBlockMarkers_RejectsDuplicateStubBegin(t *testing.T) {
	t.Parallel()

	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetGemini, "./external.md")
	content := stub + "\n" + stub
	_, _, err := importStubBlockMarkers.findRegion(content)
	if err == nil || !strings.Contains(err.Error(), "multiple Traceary managed memory blocks") {
		t.Fatalf("findRegion err = %v, want duplicate-begin rejection", err)
	}
}

func TestImportStubBlockMarkers_RejectsOrphanStubBegin(t *testing.T) {
	t.Parallel()

	content := "<!-- traceary-memory-import:begin:v1 -->\n@./external.md\n"
	_, _, err := importStubBlockMarkers.findRegion(content)
	if err == nil || !strings.Contains(err.Error(), "without end marker") {
		t.Fatalf("findRegion err = %v, want orphan-begin rejection", err)
	}
}

func TestImportStubBlockMarkers_DistinctFromMemoryBridgeBlockMarkers(t *testing.T) {
	t.Parallel()

	// A canonical traceary-memories block must NOT be recognized as a
	// stub region (and vice versa) so the host context file and the
	// external memory file never get mistaken for one another by the
	// parser.
	memoryBlock := MemoryBridgeMarkerBegin + "\nbody\n" + MemoryBridgeMarkerEnd + "\n"
	if _, found, err := importStubBlockMarkers.findRegion(memoryBlock); err != nil || found {
		t.Fatalf("importStubBlockMarkers.findRegion(memoryBlock) = found=%v err=%v, want found=false err=nil", found, err)
	}
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./external.md")
	if _, found, err := memoryBridgeBlockMarkers.findRegion(stub); err != nil || found {
		t.Fatalf("memoryBridgeBlockMarkers.findRegion(stub) = found=%v err=%v, want found=false err=nil", found, err)
	}
}
