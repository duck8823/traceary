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

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
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
