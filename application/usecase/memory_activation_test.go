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
	if plan.ActivatedCount != 2 {
		t.Fatalf("ActivatedCount = %d, want 2", plan.ActivatedCount)
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

func TestMemoryUsecase_ActivatePlan_RejectsUnsupportedTargetWithPathOverride(t *testing.T) {
	t.Parallel()

	sut := usecase.NewMemoryUsecase(nil, &stubExportMemoryQuery{}, nil)
	_, err := sut.ActivatePlan(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Path:   filepath.Join(t.TempDir(), "CLAUDE.md"),
	})
	if err == nil || !strings.Contains(err.Error(), "not supported yet") {
		t.Fatalf("ActivatePlan error = %v, want unsupported target", err)
	}
}
func TestMemoryUsecase_Activate_CreatesCodexTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m1", domtypes.MemoryTypePreference, scope, "prefer concise PRs"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	result, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Root:   filepath.Join(root, "nested"),
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if result.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("Action = %q, want created", result.Action)
	}
	if result.ActivatedCount != 1 {
		t.Fatalf("ActivatedCount = %d, want 1", result.ActivatedCount)
	}
	data, err := os.ReadFile(result.TargetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(data); !strings.Contains(got, usecase.MemoryBridgeMarkerBegin) || !strings.Contains(got, "prefer concise PRs") {
		t.Fatalf("target file missing managed memory: %q", got)
	}
}

func TestMemoryUsecase_Activate_ReplacesManagedBlockAndPreservesUserContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "traceary.md")
	existing := strings.Join([]string{
		"# User-authored memory",
		"",
		"- keep this note",
		usecase.MemoryBridgeMarkerBegin,
		"old managed content",
		usecase.MemoryBridgeMarkerEnd,
		"",
		"afterword",
		"",
	}, "\n")
	if err := os.WriteFile(targetPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m2", domtypes.MemoryTypeDecision, scope, "use Traceary as source of truth"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	result, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if result.Action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("Action = %q, want updated", result.Action)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	for _, want := range []string{"# User-authored memory", "- keep this note", "afterword", "use Traceary as source of truth"} {
		if !strings.Contains(got, want) {
			t.Fatalf("target file missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "old managed content") {
		t.Fatalf("old managed block was not replaced: %q", got)
	}
}

func TestMemoryUsecase_Activate_AppendsManagedBlockWhenNoExistingBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "traceary.md")
	if err := os.WriteFile(targetPath, []byte("# User content\n\n- manual Codex note\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m3", domtypes.MemoryTypeConstraint, scope, "always preserve user-authored shards"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	result, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if result.Action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("Action = %q, want updated", result.Action)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "# User content\n\n- manual Codex note\n\n"+usecase.MemoryBridgeMarkerBegin) {
		t.Fatalf("managed block was not appended after preserving user content: %q", got)
	}
}

func TestMemoryUsecase_Activate_AppendsManagedBlockWhenOnlyEndMarkerExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "traceary.md")
	existing := "# User content\n\n- docs mention " + usecase.MemoryBridgeMarkerEnd + " literally\n"
	if err := os.WriteFile(targetPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-end-marker", domtypes.MemoryTypeConstraint, scope, "orphan end markers should stay user content"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	result, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if result.Action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("Action = %q, want updated", result.Action)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, existing) || !strings.Contains(got, "orphan end markers should stay user content") {
		t.Fatalf("activation should preserve orphan end marker content and append managed block, got %q", got)
	}
}

func TestMemoryUsecase_Activate_IsIdempotentWhenContentUnchanged(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m4", domtypes.MemoryTypeLesson, scope, "rerunning activation should be a no-op"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	criteria := apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	}
	first, err := sut.Activate(context.Background(), criteria)
	if err != nil {
		t.Fatalf("Activate first: %v", err)
	}
	second, err := sut.Activate(context.Background(), criteria)
	if err != nil {
		t.Fatalf("Activate second: %v", err)
	}
	if first.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("first Action = %q, want created", first.Action)
	}
	if second.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("second Action = %q, want noop", second.Action)
	}
}

func TestMemoryUsecase_Activate_RefusesNewerManagedBlockVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "traceary.md")
	existing := "<!-- traceary-memories:begin:v99 -->\nfuture content\n" + usecase.MemoryBridgeMarkerEnd + "\n"
	if err := os.WriteFile(targetPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m5", domtypes.MemoryTypePreference, scope, "prefer safe activation"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	_, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err == nil {
		t.Fatalf("expected newer managed block refusal")
	}
	data, readErr := os.ReadFile(targetPath)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != existing {
		t.Fatalf("newer managed block file mutated: %q", string(data))
	}
}

func TestMemoryUsecase_Activate_RejectsDanglingSymlinkTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "traceary.md")
	missingTarget := filepath.Join(dir, "missing.md")
	if err := os.Symlink(missingTarget, targetPath); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-dangling-symlink", domtypes.MemoryTypePreference, scope, "reject dangling symlink activation targets"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	_, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err == nil || !strings.Contains(err.Error(), "symlinks are not supported") {
		t.Fatalf("Activate error = %v, want symlink rejection", err)
	}
	info, statErr := os.Lstat(targetPath)
	if statErr != nil {
		t.Fatalf("Lstat: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("activation replaced dangling symlink with mode %s", info.Mode())
	}
}

func TestMemoryUsecase_ActivationStatus_MissingFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m6", domtypes.MemoryTypePreference, scope, "prefer visible activation status"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	status, err := sut.ActivationStatus(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusMissing || status.Existing {
		t.Fatalf("status = %+v, want missing/non-existing", status)
	}
	if status.TargetPath != filepath.Join(root, "traceary.md") {
		t.Fatalf("TargetPath = %q, want root target", status.TargetPath)
	}
	if status.ActivatedCount != 1 {
		t.Fatalf("ActivatedCount = %d, want 1", status.ActivatedCount)
	}
}

func TestMemoryUsecase_ActivationStatus_InSyncManagedBlock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m7", domtypes.MemoryTypePreference, scope, "prefer status checks before release"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	criteria := apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	}
	if _, err := sut.Activate(context.Background(), criteria); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	status, err := sut.ActivationStatus(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusInSync || !status.Existing {
		t.Fatalf("status = %+v, want in_sync/existing", status)
	}
}

func TestMemoryUsecase_ActivationStatus_StaleManagedBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "traceary.md")
	existing := usecase.MemoryBridgeMarkerBegin + "\nold managed content\n" + usecase.MemoryBridgeMarkerEnd + "\n"
	if err := os.WriteFile(targetPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m8", domtypes.MemoryTypePreference, scope, "prefer fresh activation"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	status, err := sut.ActivationStatus(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusStale || !status.Existing {
		t.Fatalf("status = %+v, want stale/existing", status)
	}
}

func TestMemoryUsecase_ActivationStatus_InvalidManagedBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "traceary.md")
	existing := usecase.MemoryBridgeMarkerBegin + "\nunterminated managed content\n"
	if err := os.WriteFile(targetPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m9", domtypes.MemoryTypePreference, scope, "prefer invalid block warnings"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	status, err := sut.ActivationStatus(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusInvalid || !strings.Contains(status.Message, "without end marker") {
		t.Fatalf("status = %+v, want invalid unterminated block", status)
	}
}

func TestMemoryUsecase_ActivationStatus_InvalidDanglingSymlinkTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "traceary.md")
	missingTarget := filepath.Join(dir, "missing.md")
	if err := os.Symlink(missingTarget, targetPath); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-status-symlink", domtypes.MemoryTypePreference, scope, "status should reject symlink activation targets"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	status, err := sut.ActivationStatus(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   targetPath,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusInvalid || !strings.Contains(status.Message, "symlinks are not supported") {
		t.Fatalf("status = %+v, want invalid symlink target", status)
	}
}

func mustWorkspaceScope(t *testing.T, value string) domtypes.MemoryScope {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom(value)
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	return domtypes.WorkspaceScopeOf(workspace)
}
