package usecase_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemoryUsecase_ActivatePlan_DefaultCodexTargetIsDryRunOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	workspaceScope := domtypes.WorkspaceScopeOf(workspace)
	globalScope := domtypes.GlobalScopeOf()
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-global", domtypes.MemoryTypeConstraint, globalScope, "always request Codex review"),
			mustAcceptedSummary(t, "m-workspace", domtypes.MemoryTypePreference, workspaceScope, "prefer concise PRs"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	plan, err := sut.ActivatePlan(context.Background(), apptypes.MemoryActivationCriteria{
		Target:        apptypes.MemoryBridgeTargetCodex,
		Root:          root,
		Scopes:        []domtypes.MemoryScope{workspaceScope},
		IncludeGlobal: true,
	})
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	if want := filepath.Join(root, "traceary.md"); plan.TargetPath != want {
		t.Fatalf("TargetPath = %q, want %q", plan.TargetPath, want)
	}
	if _, err := os.Stat(plan.TargetPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run plan must not create the target file, stat err = %v", err)
	}
	assertMemoryScopes(t, query.calls[0].Scopes(), []domtypes.MemoryScope{workspaceScope, globalScope})
	if !strings.Contains(plan.Markdown, usecase.MemoryBridgeMarkerBegin) || !strings.Contains(plan.Markdown, "## Global memories") {
		t.Fatalf("planned markdown missing managed markers/global section: %q", plan.Markdown)
	}
}

func TestMemoryUsecase_ActivatePlan_PathOverrideAndExistingDiff(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "custom.md")
	if err := os.WriteFile(targetPath, []byte("old content\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m1", domtypes.MemoryTypePreference, scope, "prefer concise PRs"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	plan, err := sut.ActivatePlan(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
		Diff:   true,
	})
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	if !plan.Existing {
		t.Fatalf("Existing = false, want true")
	}
	if plan.TargetPath != targetPath {
		t.Fatalf("TargetPath = %q, want path override %q", plan.TargetPath, targetPath)
	}
	if !strings.Contains(plan.Diff, "--- "+targetPath) || !strings.Contains(plan.Diff, "-old content") || !strings.Contains(plan.Diff, "+<!-- traceary-memories:begin:v1 -->") {
		t.Fatalf("unexpected diff: %q", plan.Diff)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "old content\n" {
		t.Fatalf("dry-run diff must not mutate existing file, got %q", string(data))
	}
}
