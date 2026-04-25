package usecase_test

import (
	"context"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemoryBridgeImport_ProposesBulletsOutsideMarkers(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}

	content := strings.Join([]string{
		"# Project instructions",
		"",
		"- prefer bulleted commit messages",
		"- always update docs in the same PR",
		"",
		usecase.MemoryBridgeMarkerBegin,
		"## Preferences",
		"- already managed — do not import",
		usecase.MemoryBridgeMarkerEnd,
		"",
		"- always run go tool golangci-lint run",
	}, "\n")

	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}
	sut := usecase.NewMemoryBridgeImportUsecase(memoryStub, querySvc, nil)

	result, err := sut.ImportInstructions(context.Background(), apptypes.MemoryBridgeImportCriteria{
		Target:            apptypes.MemoryBridgeTargetClaude,
		Markdown:          content,
		Path:              "/tmp/CLAUDE.md",
		WorkspaceFallback: workspace,
	})
	if err != nil {
		t.Fatalf("ImportInstructions: %v", err)
	}

	if len(memoryStub.proposeCalls) != 3 {
		t.Fatalf("expected 3 propose calls (bullets outside marker), got %d", len(memoryStub.proposeCalls))
	}
	for _, call := range memoryStub.proposeCalls {
		if call.source != domtypes.MemorySourceImported {
			t.Fatalf("source = %q, want imported", call.source)
		}
		if strings.Contains(call.fact, "already managed") {
			t.Fatalf("managed block should not be re-imported, got %q", call.fact)
		}
	}
	if result.SkippedDuplicateCount != 0 || result.SkippedRejectedCount != 0 {
		t.Fatalf("unexpected skips: dup=%d rejected=%d", result.SkippedDuplicateCount, result.SkippedRejectedCount)
	}
}

func TestMemoryBridgeImport_FutureMarkerVersionStillSkipsManagedBlock(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}

	// A future Traceary build wrote the block as :v42; the current build
	// must still treat it as managed and skip the bullets inside (no
	// duplicate candidate), and it must emit a warning telling the
	// operator not to overwrite the newer block with this older binary.
	content := strings.Join([]string{
		"# Project instructions",
		"",
		"- free-form bullet that should be imported",
		"",
		"<!-- traceary-memories:begin:v42 -->",
		"- managed-only bullet; do not re-import",
		"<!-- traceary-memories:end -->",
	}, "\n")

	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}
	sut := usecase.NewMemoryBridgeImportUsecase(memoryStub, querySvc, nil)

	result, err := sut.ImportInstructions(context.Background(), apptypes.MemoryBridgeImportCriteria{
		Target:            apptypes.MemoryBridgeTargetClaude,
		Markdown:          content,
		Path:              "/tmp/CLAUDE.md",
		WorkspaceFallback: workspace,
	})
	if err != nil {
		t.Fatalf("ImportInstructions: %v", err)
	}

	if len(memoryStub.proposeCalls) != 1 {
		t.Fatalf("expected 1 propose call for the free-form bullet, got %d", len(memoryStub.proposeCalls))
	}
	if strings.Contains(memoryStub.proposeCalls[0].fact, "managed-only") {
		t.Fatalf("managed block (v42) leaked into proposed candidates: %q", memoryStub.proposeCalls[0].fact)
	}
	foundWarning := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "v42") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected a warning about marker version v42, got %+v", result.Warnings)
	}
}

func TestMemoryBridgeImport_MissingWorkspaceEmitsWarning(t *testing.T) {
	t.Parallel()

	content := "# Project\n\n- sample bullet\n"
	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}
	sut := usecase.NewMemoryBridgeImportUsecase(memoryStub, querySvc, nil)

	result, err := sut.ImportInstructions(context.Background(), apptypes.MemoryBridgeImportCriteria{
		Target:   apptypes.MemoryBridgeTargetClaude,
		Markdown: content,
	})
	if err != nil {
		t.Fatalf("ImportInstructions: %v", err)
	}
	if len(memoryStub.proposeCalls) != 0 {
		t.Fatalf("expected no propose calls without workspace, got %d", len(memoryStub.proposeCalls))
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected a warning about missing --workspace")
	}
}
