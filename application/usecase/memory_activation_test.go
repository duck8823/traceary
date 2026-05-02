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

func TestMemoryUsecase_ActivatePlan_RejectsGeminiUntilLaterIssue(t *testing.T) {
	t.Parallel()

	sut := usecase.NewMemoryUsecase(nil, &stubExportMemoryQuery{}, nil)
	_, err := sut.ActivatePlan(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetGemini,
		Path:   filepath.Join(t.TempDir(), "GEMINI.md"),
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

func TestMemoryUsecase_ActivatePlan_ClaudeMissingPairExposesComponents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-plan", domtypes.MemoryTypePreference, scope, "prefer concise PRs"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	plan, err := sut.ActivatePlan(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	if plan.HostContext == nil || plan.ExternalMemory == nil {
		t.Fatalf("two-file plan must expose components, got %+v", plan)
	}
	wantHost := filepath.Join(root, "CLAUDE.md")
	wantExternal := filepath.Join(root, ".traceary", "memories", "claude.md")
	if plan.HostContext.Path != wantHost {
		t.Fatalf("HostContext.Path = %q, want %q", plan.HostContext.Path, wantHost)
	}
	if plan.ExternalMemory.Path != wantExternal {
		t.Fatalf("ExternalMemory.Path = %q, want %q", plan.ExternalMemory.Path, wantExternal)
	}
	if plan.HostContext.State != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("HostContext.State = %q, want missing", plan.HostContext.State)
	}
	if plan.ExternalMemory.State != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("ExternalMemory.State = %q, want missing", plan.ExternalMemory.State)
	}
	if !strings.Contains(plan.HostContext.Markdown, "@./.traceary/memories/claude.md") {
		t.Fatalf("HostContext.Markdown missing relative import line: %q", plan.HostContext.Markdown)
	}
	if !strings.Contains(plan.ExternalMemory.Markdown, "prefer concise PRs") {
		t.Fatalf("ExternalMemory.Markdown missing accepted memory: %q", plan.ExternalMemory.Markdown)
	}
	if _, err := os.Stat(wantHost); !os.IsNotExist(err) {
		t.Fatalf("dry-run plan must not create CLAUDE.md, stat err = %v", err)
	}
	if _, err := os.Stat(wantExternal); !os.IsNotExist(err) {
		t.Fatalf("dry-run plan must not create external memory file, stat err = %v", err)
	}
}

func TestMemoryUsecase_ActivatePlan_ClaudeDiffRendersDeterministically(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	if err := os.WriteFile(hostPath, []byte("# user notes\n\n<!-- traceary-memory-import:begin:v1 -->\n@./old.md\n<!-- traceary-memory-import:end -->\n"), 0o600); err != nil {
		t.Fatalf("WriteFile host: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(externalPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(externalPath, []byte(usecase.MemoryBridgeMarkerBegin+"\nold body\n"+usecase.MemoryBridgeMarkerEnd+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-diff", domtypes.MemoryTypePreference, scope, "prefer fresh activation"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	plan, err := sut.ActivatePlan(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
		Diff:   true,
	})
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	if plan.ExternalMemory.Diff == "" {
		t.Fatalf("ExternalMemory.Diff is empty, want stale-content diff")
	}
	if plan.HostContext.Diff == "" {
		t.Fatalf("HostContext.Diff is empty, want stub diff")
	}
	// The aggregate diff must order external before host so dry-run output
	// matches the documented apply order.
	externalIdx := strings.Index(plan.Diff, "--- "+externalPath)
	hostIdx := strings.Index(plan.Diff, "--- "+hostPath)
	if externalIdx < 0 || hostIdx < 0 || externalIdx >= hostIdx {
		t.Fatalf("Plan.Diff must order external before host, externalIdx=%d hostIdx=%d diff=%q", externalIdx, hostIdx, plan.Diff)
	}
	// The aggregate diff must keep a readable boundary between the two
	// per-component diffs. Each component diff ends in "\n", and joining
	// with "\n" produces a blank line that separates the headers.
	if !strings.Contains(plan.Diff, plan.ExternalMemory.Diff+"\n--- "+hostPath) {
		t.Fatalf("Plan.Diff must separate external and host diffs with a blank line, got %q", plan.Diff)
	}

	plan2, err := sut.ActivatePlan(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
		Diff:   true,
	})
	if err != nil {
		t.Fatalf("ActivatePlan second: %v", err)
	}
	if plan.Diff != plan2.Diff {
		t.Fatalf("diff is not deterministic across runs")
	}
}

func TestMemoryUsecase_Activate_ClaudeCreatesPair(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-apply", domtypes.MemoryTypePreference, scope, "prefer concise PRs"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	result, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if result.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("Action = %q, want created", result.Action)
	}
	if result.HostContext == nil || result.ExternalMemory == nil {
		t.Fatalf("Claude result must expose HostContext and ExternalMemory components, got %+v", result)
	}
	if result.HostContext.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("HostContext.Action = %q, want created", result.HostContext.Action)
	}
	if result.ExternalMemory.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("ExternalMemory.Action = %q, want created", result.ExternalMemory.Action)
	}
	if result.HostContext.State != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("HostContext.State = %q, want in_sync after apply", result.HostContext.State)
	}
	if result.ExternalMemory.State != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("ExternalMemory.State = %q, want in_sync after apply", result.ExternalMemory.State)
	}
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	if result.HostContext.Path != hostPath {
		t.Fatalf("HostContext.Path = %q, want %q", result.HostContext.Path, hostPath)
	}
	if result.ExternalMemory.Path != externalPath {
		t.Fatalf("ExternalMemory.Path = %q, want %q", result.ExternalMemory.Path, externalPath)
	}
	hostBody, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("ReadFile host: %v", err)
	}
	if !strings.Contains(string(hostBody), "<!-- traceary-memory-import:begin:v1 -->") {
		t.Fatalf("CLAUDE.md missing import stub: %q", string(hostBody))
	}
	if !strings.Contains(string(hostBody), "@./.traceary/memories/claude.md") {
		t.Fatalf("CLAUDE.md missing relative import line: %q", string(hostBody))
	}
	externalBody, err := os.ReadFile(externalPath)
	if err != nil {
		t.Fatalf("ReadFile external: %v", err)
	}
	if !strings.Contains(string(externalBody), usecase.MemoryBridgeMarkerBegin) {
		t.Fatalf("external memory file missing managed block: %q", string(externalBody))
	}
	if !strings.Contains(string(externalBody), "prefer concise PRs") {
		t.Fatalf("external memory file missing accepted memory: %q", string(externalBody))
	}
}

func TestMemoryUsecase_Activate_ClaudePreservesUserAuthoredHostContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	userBody := "# Project rules\n\n- always run `make test` before commit\n- prefer plain markdown\n"
	if err := os.WriteFile(hostPath, []byte(userBody), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-preserve", domtypes.MemoryTypeDecision, scope, "use Traceary as memory source"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	result, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if result.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("Action = %q, want created (external file new + host stub appended)", result.Action)
	}
	hostBody, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("ReadFile host: %v", err)
	}
	got := string(hostBody)
	for _, want := range []string{"# Project rules", "- always run `make test` before commit", "- prefer plain markdown", "<!-- traceary-memory-import:begin:v1 -->"} {
		if !strings.Contains(got, want) {
			t.Fatalf("CLAUDE.md missing %q after apply: %q", want, got)
		}
	}
	if !strings.HasPrefix(got, userBody) {
		t.Fatalf("user-authored content was not preserved at the top of CLAUDE.md: %q", got)
	}
}

func TestMemoryUsecase_Activate_ClaudeRefreshesStaleExternal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-refresh", domtypes.MemoryTypePreference, scope, "prefer fresh activation projection"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	criteria := apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	}

	if _, err := sut.Activate(context.Background(), criteria); err != nil {
		t.Fatalf("first Activate: %v", err)
	}
	if err := os.WriteFile(externalPath, []byte("preface\n\n"+usecase.MemoryBridgeMarkerBegin+"\nstale body\n"+usecase.MemoryBridgeMarkerEnd+"\n\nepilogue\n"), 0o600); err != nil {
		t.Fatalf("WriteFile stale external: %v", err)
	}

	result, err := sut.Activate(context.Background(), criteria)
	if err != nil {
		t.Fatalf("second Activate: %v", err)
	}
	if result.Action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("Action = %q, want updated", result.Action)
	}
	if result.HostContext.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("HostContext.Action = %q, want noop after first apply", result.HostContext.Action)
	}
	if result.ExternalMemory.Action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("ExternalMemory.Action = %q, want updated", result.ExternalMemory.Action)
	}
	externalBody, err := os.ReadFile(externalPath)
	if err != nil {
		t.Fatalf("ReadFile external: %v", err)
	}
	got := string(externalBody)
	for _, want := range []string{"preface", "epilogue", "prefer fresh activation projection"} {
		if !strings.Contains(got, want) {
			t.Fatalf("external memory missing %q after stale refresh: %q", want, got)
		}
	}
	if strings.Contains(got, "stale body") {
		t.Fatalf("stale managed body was not replaced: %q", got)
	}
	hostBody, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("ReadFile host: %v", err)
	}
	if !strings.Contains(string(hostBody), "@./.traceary/memories/claude.md") {
		t.Fatalf("CLAUDE.md import stub regressed after refresh: %q", string(hostBody))
	}
}

func TestMemoryUsecase_Activate_ClaudeIsIdempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-idem", domtypes.MemoryTypePreference, scope, "prefer idempotent applies"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	criteria := apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	}

	first, err := sut.Activate(context.Background(), criteria)
	if err != nil {
		t.Fatalf("first Activate: %v", err)
	}
	if first.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("first Action = %q, want created", first.Action)
	}

	second, err := sut.Activate(context.Background(), criteria)
	if err != nil {
		t.Fatalf("second Activate: %v", err)
	}
	if second.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("second Action = %q, want noop", second.Action)
	}
	if second.HostContext.Action != apptypes.MemoryActivationApplyNoop || second.ExternalMemory.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("second per-component actions = host=%q external=%q, want both noop", second.HostContext.Action, second.ExternalMemory.Action)
	}
}

func TestMemoryUsecase_Activate_ClaudeRefusesInvalidHostStub(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(hostPath, []byte("<!-- traceary-memory-import:begin:v9 -->\n@./.traceary/memories/claude.md\n<!-- traceary-memory-import:end -->\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-refuse", domtypes.MemoryTypePreference, scope, "prefer safe refusals"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	_, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to apply invalid host context stub") {
		t.Fatalf("Activate err = %v, want invalid host stub refusal", err)
	}
	hostBody, readErr := os.ReadFile(hostPath)
	if readErr != nil {
		t.Fatalf("ReadFile host: %v", readErr)
	}
	if !strings.Contains(string(hostBody), "<!-- traceary-memory-import:begin:v9 -->") {
		t.Fatalf("apply must not overwrite a newer-version stub, got %q", string(hostBody))
	}
}

func TestMemoryUsecase_Activate_ClaudeRefusesSymlinkHostContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	missingTarget := filepath.Join(root, "missing-target.md")
	if err := os.Symlink(missingTarget, hostPath); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-symlink", domtypes.MemoryTypePreference, scope, "prefer symlink refusal"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	_, err := sut.Activate(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err == nil || !strings.Contains(err.Error(), "symlinks are not supported") {
		t.Fatalf("Activate err = %v, want symlink refusal", err)
	}
	info, statErr := os.Lstat(hostPath)
	if statErr != nil {
		t.Fatalf("Lstat: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("apply must not replace a dangling symlink, got mode %s", info.Mode())
	}
}

func TestMemoryUsecase_ActivationStatus_ClaudeMissingPair(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-missing", domtypes.MemoryTypePreference, scope, "prefer claude status"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	status, err := sut.ActivationStatus(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("State = %q, want missing", status.State)
	}
	if status.HostContext == nil || status.ExternalMemory == nil {
		t.Fatalf("two-file status must expose components, got %+v", status)
	}
	if status.HostContext.State != apptypes.MemoryActivationStatusMissing || status.ExternalMemory.State != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("component states = host=%q external=%q, want both missing", status.HostContext.State, status.ExternalMemory.State)
	}
	if !strings.Contains(status.Message, "stub") {
		t.Fatalf("Message = %q, want pair-aware message", status.Message)
	}
}

func TestMemoryUsecase_ActivationStatus_ClaudeStaleStubInSyncExternal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	if err := os.WriteFile(hostPath, []byte("<!-- traceary-memory-import:begin:v1 -->\n@./stale.md\n<!-- traceary-memory-import:end -->\n"), 0o600); err != nil {
		t.Fatalf("WriteFile host: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(externalPath), 0o700); err != nil {
		t.Fatalf("MkdirAll external: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-stale", domtypes.MemoryTypePreference, scope, "prefer stable stubs"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	criteria := apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	}
	plan, err := sut.ActivatePlan(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	if err := os.WriteFile(externalPath, []byte(plan.ExternalMemory.Markdown), 0o600); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}

	status, err := sut.ActivationStatus(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusStale {
		t.Fatalf("State = %q, want stale (host stale, external in_sync)", status.State)
	}
	if status.HostContext.State != apptypes.MemoryActivationStatusStale {
		t.Fatalf("HostContext.State = %q, want stale", status.HostContext.State)
	}
	if status.ExternalMemory.State != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("ExternalMemory.State = %q, want in_sync", status.ExternalMemory.State)
	}
}

func TestMemoryUsecase_ActivationStatus_ClaudeInvalidStubBeginsWithoutEnd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(hostPath, []byte("<!-- traceary-memory-import:begin:v1 -->\n@./.traceary/memories/claude.md\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-invalid", domtypes.MemoryTypePreference, scope, "prefer invalid pair detection"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	status, err := sut.ActivationStatus(context.Background(), apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	})
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusInvalid {
		t.Fatalf("State = %q, want invalid", status.State)
	}
	if status.HostContext.State != apptypes.MemoryActivationStatusInvalid {
		t.Fatalf("HostContext.State = %q, want invalid", status.HostContext.State)
	}
	if !strings.Contains(status.HostContext.Message, "without end marker") {
		t.Fatalf("HostContext.Message = %q, want orphan-begin reason", status.HostContext.Message)
	}
}

func TestMemoryUsecase_ActivationStatus_ClaudeInSyncAfterPlanWriteback(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	scope := mustWorkspaceScope(t, "github.com/example/repo")
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-claude-insync", domtypes.MemoryTypePreference, scope, "prefer in_sync claude pair"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	criteria := apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   root,
		Scopes: []domtypes.MemoryScope{scope},
	}
	plan, err := sut.ActivatePlan(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(externalPath), 0o700); err != nil {
		t.Fatalf("MkdirAll external: %v", err)
	}
	if err := os.WriteFile(externalPath, []byte(plan.ExternalMemory.Markdown), 0o600); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte(plan.HostContext.Markdown), 0o600); err != nil {
		t.Fatalf("WriteFile host: %v", err)
	}

	status, err := sut.ActivationStatus(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ActivationStatus: %v", err)
	}
	if status.State != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("State = %q, want in_sync after writing planned content", status.State)
	}
}
