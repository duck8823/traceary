package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type stubCodexSource struct {
	candidates []apptypes.ImportedMemoryCandidate
	warnings   []string
	err        error
}

func (s *stubCodexSource) Load(_ context.Context, _ apptypes.CodexImportCriteria) ([]apptypes.ImportedMemoryCandidate, []string, error) {
	return s.candidates, s.warnings, s.err
}

type stubMemoryQueryService struct {
	summaries []apptypes.MemorySummary
	details   map[domtypes.MemoryID]apptypes.MemoryDetails
	calls     []apptypes.MemoryListCriteria
}

func (s *stubMemoryQueryService) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.calls = append(s.calls, criteria)
	statuses := criteria.Statuses()
	sources := criteria.Sources()
	if len(statuses) == 0 && len(sources) == 0 {
		return s.summaries, nil
	}
	filtered := make([]apptypes.MemorySummary, 0, len(s.summaries))
	for _, summary := range s.summaries {
		if len(statuses) > 0 && !statusContains(statuses, summary.Status()) {
			continue
		}
		if len(sources) > 0 && !sourceContains(sources, summary.Source()) {
			continue
		}
		filtered = append(filtered, summary)
	}
	return filtered, nil
}

func statusContains(statuses []domtypes.MemoryStatus, target domtypes.MemoryStatus) bool {
	for _, status := range statuses {
		if status == target {
			return true
		}
	}
	return false
}

func sourceContains(sources []domtypes.MemorySource, target domtypes.MemorySource) bool {
	for _, source := range sources {
		if source == target {
			return true
		}
	}
	return false
}

func (s *stubMemoryQueryService) Search(_ context.Context, _ apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}

func (s *stubMemoryQueryService) GetDetails(_ context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	if details, ok := s.details[memoryID]; ok {
		return details, nil
	}
	return apptypes.MemoryDetails{}, nil
}

type importProposeCall struct {
	fact   string
	source domtypes.MemorySource
	scope  domtypes.MemoryScope
}

type stubImportMemoryUsecase struct {
	proposeCalls []importProposeCall
	proposeErr   error
}

func (s *stubImportMemoryUsecase) Save(_ context.Context, memory *model.Memory) error {
	s.proposeCalls = append(s.proposeCalls, importProposeCall{
		fact:   memory.Fact(),
		source: memory.Source(),
		scope:  memory.Scope(),
	})
	return s.proposeErr
}

func (s *stubImportMemoryUsecase) SaveDistillation(context.Context, *model.Memory, []*model.Memory) error {
	return nil
}

func (s *stubImportMemoryUsecase) SaveSupersession(context.Context, *model.Memory, *model.Memory) error {
	return nil
}

func (s *stubImportMemoryUsecase) FindByID(context.Context, domtypes.MemoryID) (domtypes.Optional[*model.Memory], error) {
	return domtypes.None[*model.Memory](), nil
}

func (s *stubImportMemoryUsecase) Remember(context.Context, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.Optional[domtypes.Confidence], domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *stubImportMemoryUsecase) Propose(_ context.Context, _ domtypes.MemoryType, scope domtypes.MemoryScope, fact string, source domtypes.MemorySource, _ []domtypes.EvidenceRef, _ []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	s.proposeCalls = append(s.proposeCalls, importProposeCall{fact: fact, source: source, scope: scope})
	if s.proposeErr != nil {
		return apptypes.MemoryDetails{}, s.proposeErr
	}
	return buildImportSummary(scope, fact, domtypes.MemoryStatusCandidate), nil
}

func (s *stubImportMemoryUsecase) Accept(context.Context, domtypes.MemoryID, domtypes.Optional[domtypes.Confidence]) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *stubImportMemoryUsecase) Reject(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *stubImportMemoryUsecase) AttachCandidateRefs(context.Context, domtypes.MemoryID, []domtypes.EvidenceRef, []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *stubImportMemoryUsecase) Supersede(context.Context, domtypes.MemoryID, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.Optional[domtypes.Confidence], domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef, domtypes.Optional[time.Time], domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *stubImportMemoryUsecase) Expire(context.Context, domtypes.MemoryID, domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *stubImportMemoryUsecase) SetValidity(context.Context, domtypes.MemoryID, domtypes.Optional[time.Time], domtypes.Optional[time.Time], bool) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *stubImportMemoryUsecase) List(context.Context, apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}

func (s *stubImportMemoryUsecase) Search(context.Context, apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}

func (s *stubImportMemoryUsecase) Show(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func buildImportSummary(scope domtypes.MemoryScope, fact string, status domtypes.MemoryStatus) apptypes.MemoryDetails {
	return buildImportDetails(scope, fact, status, "/tmp/MEMORY.md")
}

func buildImportDetails(scope domtypes.MemoryScope, fact string, status domtypes.MemoryStatus, sourcePath string) apptypes.MemoryDetails {
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID("memory-import-test-"+fact),
		domtypes.MemoryTypePreference,
		scope,
		fact,
		status,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceImported,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Now().UTC(),
		domtypes.None[time.Time](),
		time.Now().UTC(),
		time.Now().UTC(),
	)
	if err != nil {
		panic(err)
	}
	var artifacts []domtypes.ArtifactRef
	if sourcePath != "" {
		artifact, err := domtypes.ArtifactRefFrom(domtypes.ArtifactRefKindFile, sourcePath)
		if err != nil {
			panic(err)
		}
		artifacts = append(artifacts, artifact)
	}
	return apptypes.MemoryDetailsOf(summary, nil, artifacts)
}

func workspaceScope(t *testing.T, value string) domtypes.MemoryScope {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom(value)
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	return domtypes.WorkspaceScopeOf(workspace)
}

func importCandidate(t *testing.T, fact string, scope domtypes.MemoryScope) apptypes.ImportedMemoryCandidate {
	t.Helper()
	evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindFile, "/tmp/MEMORY.md#L1-L1")
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	artifact, err := domtypes.ArtifactRefFrom(domtypes.ArtifactRefKindFile, "/tmp/MEMORY.md")
	if err != nil {
		t.Fatalf("ArtifactRefFrom: %v", err)
	}
	return apptypes.ImportedMemoryCandidate{
		MemoryType:   domtypes.MemoryTypePreference,
		Scope:        scope,
		Fact:         fact,
		EvidenceRefs: []domtypes.EvidenceRef{evidence},
		ArtifactRefs: []domtypes.ArtifactRef{artifact},
		SourcePath:   "/tmp/MEMORY.md",
	}
}

func TestMemoryUsecase_ImportCodex_ProposesCandidates(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	source := &stubCodexSource{
		candidates: []apptypes.ImportedMemoryCandidate{
			importCandidate(t, "prefer bulleted messages", scope),
		},
	}
	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}

	sut := usecase.NewMemoryUsecase(memoryStub, querySvc, nil, usecase.MemoryUsecaseDependencies{CodexSource: source})
	result, err := sut.ImportCodex(context.Background(), apptypes.CodexImportCriteria{Root: "/tmp/codex"})
	if err != nil {
		t.Fatalf("ImportCodex: %v", err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("expected 1 imported candidate, got %d", len(result.Imported))
	}
	if len(memoryStub.proposeCalls) != 1 {
		t.Fatalf("Propose calls = %d, want 1", len(memoryStub.proposeCalls))
	}
	call := memoryStub.proposeCalls[0]
	if call.source != domtypes.MemorySourceImported {
		t.Fatalf("source = %q, want %q", call.source, domtypes.MemorySourceImported)
	}
	if call.fact != "prefer bulleted messages" {
		t.Fatalf("fact = %q, want %q", call.fact, "prefer bulleted messages")
	}
	if result.SkippedDuplicateCount != 0 || result.SkippedRejectedCount != 0 {
		t.Fatalf("unexpected skip counts: duplicate=%d rejected=%d", result.SkippedDuplicateCount, result.SkippedRejectedCount)
	}
}

func TestMemoryUsecase_ImportCodex_SkipsExistingCandidate(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	existing := buildImportDetails(scope, "prefer bulleted messages", domtypes.MemoryStatusCandidate, "/tmp/MEMORY.md")
	querySvc := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{existing.Summary()},
		details:   map[domtypes.MemoryID]apptypes.MemoryDetails{existing.Summary().MemoryID(): existing},
	}
	memoryStub := &stubImportMemoryUsecase{}
	source := &stubCodexSource{
		candidates: []apptypes.ImportedMemoryCandidate{
			importCandidate(t, "prefer bulleted messages", scope),
		},
	}

	sut := usecase.NewMemoryUsecase(memoryStub, querySvc, nil, usecase.MemoryUsecaseDependencies{CodexSource: source})
	result, err := sut.ImportCodex(context.Background(), apptypes.CodexImportCriteria{Root: "/tmp/codex"})
	if err != nil {
		t.Fatalf("ImportCodex: %v", err)
	}
	if len(memoryStub.proposeCalls) != 0 {
		t.Fatalf("Propose should not be called for duplicates, got %d", len(memoryStub.proposeCalls))
	}
	if result.SkippedDuplicateCount != 1 {
		t.Fatalf("SkippedDuplicateCount = %d, want 1", result.SkippedDuplicateCount)
	}
}

func TestMemoryUsecase_ImportCodex_DoesNotResurrectRejected(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	existing := buildImportDetails(scope, "prefer bulleted messages", domtypes.MemoryStatusRejected, "/tmp/MEMORY.md")
	querySvc := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{existing.Summary()},
		details:   map[domtypes.MemoryID]apptypes.MemoryDetails{existing.Summary().MemoryID(): existing},
	}
	memoryStub := &stubImportMemoryUsecase{}
	source := &stubCodexSource{
		candidates: []apptypes.ImportedMemoryCandidate{
			importCandidate(t, "prefer bulleted messages", scope),
		},
	}

	sut := usecase.NewMemoryUsecase(memoryStub, querySvc, nil, usecase.MemoryUsecaseDependencies{CodexSource: source})
	result, err := sut.ImportCodex(context.Background(), apptypes.CodexImportCriteria{Root: "/tmp/codex"})
	if err != nil {
		t.Fatalf("ImportCodex: %v", err)
	}
	if len(memoryStub.proposeCalls) != 0 {
		t.Fatalf("Propose should not be called when rejected exists, got %d", len(memoryStub.proposeCalls))
	}
	if result.SkippedRejectedCount != 1 {
		t.Fatalf("SkippedRejectedCount = %d, want 1", result.SkippedRejectedCount)
	}
}

// TestMemoryUsecase_ImportCodex_DifferentSourcePathIsNotDuplicate pins
// the regression that earlier collapsed memories with identical facts but
// distinct source files. With SourcePath in the dedupe key, a rejected
// memory imported from /tmp/MEMORY.md must not suppress a new candidate
// read out of /home/shared/MEMORY.md, because they represent independent
// statements from independent hosts.
func TestMemoryUsecase_ImportCodex_DifferentSourcePathIsNotDuplicate(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	existing := buildImportDetails(scope, "prefer bulleted messages", domtypes.MemoryStatusRejected, "/tmp/MEMORY.md")
	querySvc := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{existing.Summary()},
		details:   map[domtypes.MemoryID]apptypes.MemoryDetails{existing.Summary().MemoryID(): existing},
	}
	memoryStub := &stubImportMemoryUsecase{}
	otherRootCandidate := importCandidate(t, "prefer bulleted messages", scope)
	otherRootCandidate.SourcePath = "/home/shared/MEMORY.md"
	source := &stubCodexSource{candidates: []apptypes.ImportedMemoryCandidate{otherRootCandidate}}

	sut := usecase.NewMemoryUsecase(memoryStub, querySvc, nil, usecase.MemoryUsecaseDependencies{CodexSource: source})
	result, err := sut.ImportCodex(context.Background(), apptypes.CodexImportCriteria{Root: "/home/shared"})
	if err != nil {
		t.Fatalf("ImportCodex: %v", err)
	}
	if len(memoryStub.proposeCalls) != 1 {
		t.Fatalf("expected 1 propose call for the different source path, got %d", len(memoryStub.proposeCalls))
	}
	if result.SkippedRejectedCount != 0 || result.SkippedDuplicateCount != 0 {
		t.Fatalf("unexpected skips: duplicate=%d rejected=%d", result.SkippedDuplicateCount, result.SkippedRejectedCount)
	}
}

func TestMemoryUsecase_ImportCodex_RedactionAppliedToFact(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	// ghp_... style PATs are masked by the built-in audit redaction set.
	candidate := importCandidate(t, "token=ghp_abcdefghijklmnopqrstuvwxyz0123456789", scope)
	source := &stubCodexSource{candidates: []apptypes.ImportedMemoryCandidate{candidate}}
	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}

	sut := usecase.NewMemoryUsecase(memoryStub, querySvc, nil, usecase.MemoryUsecaseDependencies{CodexSource: source})
	_, err := sut.ImportCodex(context.Background(), apptypes.CodexImportCriteria{Root: "/tmp/codex"})
	if err != nil {
		t.Fatalf("ImportCodex: %v", err)
	}
	if len(memoryStub.proposeCalls) != 1 {
		t.Fatalf("expected 1 propose call, got %d", len(memoryStub.proposeCalls))
	}
	redacted := memoryStub.proposeCalls[0].fact
	if redacted == candidate.Fact {
		t.Fatalf("sanitizer should have masked the fact, got %q", redacted)
	}
}

func TestMemoryUsecase_ImportCodex_PropagatesSourceError(t *testing.T) {
	t.Parallel()

	source := &stubCodexSource{err: errors.New("boom")}
	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}

	sut := usecase.NewMemoryUsecase(memoryStub, querySvc, nil, usecase.MemoryUsecaseDependencies{CodexSource: source})
	_, err := sut.ImportCodex(context.Background(), apptypes.CodexImportCriteria{Root: "/tmp/codex"})
	if err == nil {
		t.Fatalf("expected error from source failure")
	}
}
