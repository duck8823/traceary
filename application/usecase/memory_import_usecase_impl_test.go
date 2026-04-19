package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
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
	calls     []apptypes.MemoryListCriteria
}

func (s *stubMemoryQueryService) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.calls = append(s.calls, criteria)
	return s.summaries, nil
}

func (s *stubMemoryQueryService) Search(_ context.Context, _ apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}

func (s *stubMemoryQueryService) GetDetails(_ context.Context, _ domtypes.MemoryID) (apptypes.MemoryDetails, error) {
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

func (s *stubImportMemoryUsecase) Supersede(context.Context, domtypes.MemoryID, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.Optional[domtypes.Confidence], domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *stubImportMemoryUsecase) Expire(context.Context, domtypes.MemoryID, domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error) {
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
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID("memory-import-test"),
		domtypes.MemoryTypePreference,
		scope,
		fact,
		status,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceImported,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Now().UTC(),
		time.Now().UTC(),
	)
	if err != nil {
		panic(err)
	}
	return apptypes.MemoryDetailsOf(summary, nil, nil)
}

func workspaceScope(t *testing.T, value string) domtypes.MemoryScope {
	t.Helper()
	workspace, err := domtypes.WorkspaceOf(value)
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	return domtypes.WorkspaceScopeOf(workspace)
}

func importCandidate(t *testing.T, fact string, scope domtypes.MemoryScope) apptypes.ImportedMemoryCandidate {
	t.Helper()
	evidence, err := domtypes.EvidenceRefOf(domtypes.EvidenceRefKindFile, "/tmp/MEMORY.md#L1-L1")
	if err != nil {
		t.Fatalf("EvidenceRefOf: %v", err)
	}
	artifact, err := domtypes.ArtifactRefOf(domtypes.ArtifactRefKindFile, "/tmp/MEMORY.md")
	if err != nil {
		t.Fatalf("ArtifactRefOf: %v", err)
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

func TestMemoryImportUsecase_ImportCodex_ProposesCandidates(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	source := &stubCodexSource{
		candidates: []apptypes.ImportedMemoryCandidate{
			importCandidate(t, "prefer bulleted messages", scope),
		},
	}
	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}

	sut := usecase.NewMemoryImportUsecase(memoryStub, querySvc, source, nil)
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

func TestMemoryImportUsecase_ImportCodex_SkipsExistingCandidate(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	existing := buildImportSummary(scope, "prefer bulleted messages", domtypes.MemoryStatusCandidate).Summary()
	querySvc := &stubMemoryQueryService{summaries: []apptypes.MemorySummary{existing}}
	memoryStub := &stubImportMemoryUsecase{}
	source := &stubCodexSource{
		candidates: []apptypes.ImportedMemoryCandidate{
			importCandidate(t, "prefer bulleted messages", scope),
		},
	}

	sut := usecase.NewMemoryImportUsecase(memoryStub, querySvc, source, nil)
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

func TestMemoryImportUsecase_ImportCodex_DoesNotResurrectRejected(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	existing := buildImportSummary(scope, "prefer bulleted messages", domtypes.MemoryStatusRejected).Summary()
	querySvc := &stubMemoryQueryService{summaries: []apptypes.MemorySummary{existing}}
	memoryStub := &stubImportMemoryUsecase{}
	source := &stubCodexSource{
		candidates: []apptypes.ImportedMemoryCandidate{
			importCandidate(t, "prefer bulleted messages", scope),
		},
	}

	sut := usecase.NewMemoryImportUsecase(memoryStub, querySvc, source, nil)
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

func TestMemoryImportUsecase_ImportCodex_RedactionAppliedToFact(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	// ghp_... style PATs are masked by the built-in audit redaction set.
	candidate := importCandidate(t, "token=ghp_abcdefghijklmnopqrstuvwxyz0123456789", scope)
	source := &stubCodexSource{candidates: []apptypes.ImportedMemoryCandidate{candidate}}
	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}

	sut := usecase.NewMemoryImportUsecase(memoryStub, querySvc, source, nil)
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

func TestMemoryImportUsecase_ImportCodex_PropagatesSourceError(t *testing.T) {
	t.Parallel()

	source := &stubCodexSource{err: errors.New("boom")}
	memoryStub := &stubImportMemoryUsecase{}
	querySvc := &stubMemoryQueryService{}

	sut := usecase.NewMemoryImportUsecase(memoryStub, querySvc, source, nil)
	_, err := sut.ImportCodex(context.Background(), apptypes.CodexImportCriteria{Root: "/tmp/codex"})
	if err == nil {
		t.Fatalf("expected error from source failure")
	}
}
