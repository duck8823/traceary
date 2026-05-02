package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type stubExportMemoryQuery struct {
	summaries []apptypes.MemorySummary
	calls     []apptypes.MemoryListCriteria
}

func (s *stubExportMemoryQuery) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.calls = append(s.calls, criteria)
	return s.summaries, nil
}

func (s *stubExportMemoryQuery) Search(_ context.Context, _ apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}

func (s *stubExportMemoryQuery) GetDetails(_ context.Context, _ domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func mustAcceptedSummary(t *testing.T, id string, memoryType domtypes.MemoryType, scope domtypes.MemoryScope, fact string) apptypes.MemorySummary {
	t.Helper()
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		memoryType,
		scope,
		fact,
		domtypes.MemoryStatusAccepted,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}

func TestMemoryUsecase_Export_RendersStableMarkdown(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	globalScope := domtypes.GlobalScopeOf()
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m0", domtypes.MemoryTypeConstraint, globalScope, "always request Codex review"),
			mustAcceptedSummary(t, "m1", domtypes.MemoryTypeDecision, scope, "use SQLite for storage"),
			mustAcceptedSummary(t, "m2", domtypes.MemoryTypePreference, scope, "prefer bulleted commits"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	result, err := sut.Export(context.Background(), apptypes.MemoryExportCriteria{
		Target:        apptypes.MemoryBridgeTargetClaude,
		Scopes:        []domtypes.MemoryScope{scope},
		IncludeGlobal: true,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if result.ExportedCount != 3 {
		t.Fatalf("expected 3 exported memories, got %d", result.ExportedCount)
	}
	if len(query.calls) != 1 {
		t.Fatalf("expected one query call, got %d", len(query.calls))
	}
	assertMemoryScopes(t, query.calls[0].Scopes(), []domtypes.MemoryScope{scope, globalScope})
	assertMemoryScopes(t, result.Scopes, []domtypes.MemoryScope{scope, globalScope})
	if !strings.Contains(result.Markdown, "## Global memories") {
		t.Fatalf("expected Global memories section, got %q", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "## Workspace memories: github.com/example/repo") {
		t.Fatalf("expected Workspace memories section, got %q", result.Markdown)
	}
	if !strings.Contains(result.Markdown, usecase.MemoryBridgeMarkerBegin) {
		t.Fatalf("output missing begin marker: %q", result.Markdown)
	}
	if !strings.Contains(result.Markdown, usecase.MemoryBridgeMarkerEnd) {
		t.Fatalf("output missing end marker: %q", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "## Preferences") {
		t.Fatalf("expected Preferences section, got %q", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "use SQLite for storage") {
		t.Fatalf("decision fact missing, got %q", result.Markdown)
	}

	// Export must be idempotent — same summaries produce the same markdown.
	result2, err := sut.Export(context.Background(), apptypes.MemoryExportCriteria{
		Target:        apptypes.MemoryBridgeTargetClaude,
		Scopes:        []domtypes.MemoryScope{scope},
		IncludeGlobal: true,
	})
	if err != nil {
		t.Fatalf("Export (second run): %v", err)
	}
	if result.Markdown != result2.Markdown {
		t.Fatalf("export is not idempotent")
	}
}

func TestMemoryUsecase_Export_NoGlobalOptOutKeepsExplicitScopeOnly(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m1", domtypes.MemoryTypePreference, scope, "prefer bulleted commits"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	result, err := sut.Export(context.Background(), apptypes.MemoryExportCriteria{
		Target:        apptypes.MemoryBridgeTargetCodex,
		Scopes:        []domtypes.MemoryScope{scope},
		IncludeGlobal: false,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(query.calls) != 1 {
		t.Fatalf("expected one query call, got %d", len(query.calls))
	}
	assertMemoryScopes(t, query.calls[0].Scopes(), []domtypes.MemoryScope{scope})
	assertMemoryScopes(t, result.Scopes, []domtypes.MemoryScope{scope})
}

func TestMemoryUsecase_Export_RootWorkspaceCanRenderGlobalMemories(t *testing.T) {
	t.Parallel()

	rootWorkspace, err := domtypes.WorkspaceFrom("/")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	rootScope := domtypes.WorkspaceScopeOf(rootWorkspace)
	globalScope := domtypes.GlobalScopeOf()
	query := &stubExportMemoryQuery{
		summaries: []apptypes.MemorySummary{
			mustAcceptedSummary(t, "m-global", domtypes.MemoryTypeConstraint, globalScope, "always request Codex review"),
		},
	}
	sut := usecase.NewMemoryUsecase(nil, query, nil)

	result, err := sut.Export(context.Background(), apptypes.MemoryExportCriteria{
		Target:        apptypes.MemoryBridgeTargetCodex,
		Scopes:        []domtypes.MemoryScope{rootScope},
		IncludeGlobal: true,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	assertMemoryScopes(t, query.calls[0].Scopes(), []domtypes.MemoryScope{rootScope, globalScope})
	if result.ExportedCount != 1 {
		t.Fatalf("ExportedCount = %d, want 1", result.ExportedCount)
	}
	if !strings.Contains(result.Markdown, "## Global memories") || !strings.Contains(result.Markdown, "always request Codex review") {
		t.Fatalf("global memory missing from root workspace export: %q", result.Markdown)
	}
}

func TestMemoryUsecase_Export_EmptyExportStillEmitsMarkers(t *testing.T) {
	t.Parallel()

	query := &stubExportMemoryQuery{}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	result, err := sut.Export(context.Background(), apptypes.MemoryExportCriteria{Target: apptypes.MemoryBridgeTargetCodex})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if result.ExportedCount != 0 {
		t.Fatalf("ExportedCount = %d, want 0", result.ExportedCount)
	}
	if !strings.Contains(result.Markdown, usecase.MemoryBridgeMarkerBegin) || !strings.Contains(result.Markdown, usecase.MemoryBridgeMarkerEnd) {
		t.Fatalf("empty export must still emit markers so the next import can round-trip, got %q", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "No accepted durable memories matched") {
		t.Fatalf("expected empty body placeholder, got %q", result.Markdown)
	}
}

func TestMemoryUsecase_Export_RejectsUnknownTarget(t *testing.T) {
	t.Parallel()

	query := &stubExportMemoryQuery{}
	sut := usecase.NewMemoryUsecase(nil, query, nil)
	_, err := sut.Export(context.Background(), apptypes.MemoryExportCriteria{Target: apptypes.MemoryBridgeTarget("unknown")})
	if err == nil {
		t.Fatalf("expected error for unknown target")
	}
}

func assertMemoryScopes(t *testing.T, got []domtypes.MemoryScope, want []domtypes.MemoryScope) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("scope length = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Kind() != want[i].Kind() || got[i].Key() != want[i].Key() {
			t.Fatalf("scope[%d] = %s:%s, want %s:%s", i, got[i].Kind(), got[i].Key(), want[i].Kind(), want[i].Key())
		}
	}
}
