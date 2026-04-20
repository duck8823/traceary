package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryRepositoryStub struct {
	byID              map[string]*model.Memory
	saveCalls         []*model.Memory
	supersessionCalls []memorySupersessionCall
	saveErr           error
	findErr           error
}

func (s *memoryRepositoryStub) Save(_ context.Context, memory *model.Memory) error {
	if s.byID == nil {
		s.byID = make(map[string]*model.Memory)
	}
	s.saveCalls = append(s.saveCalls, memory)
	s.byID[memory.MemoryID().String()] = memory
	return s.saveErr
}

type memorySupersessionCall struct {
	superseded  *model.Memory
	replacement *model.Memory
}

func (s *memoryRepositoryStub) SaveSupersession(_ context.Context, superseded *model.Memory, replacement *model.Memory) error {
	if s.byID == nil {
		s.byID = make(map[string]*model.Memory)
	}
	s.supersessionCalls = append(s.supersessionCalls, memorySupersessionCall{
		superseded:  superseded,
		replacement: replacement,
	})
	s.byID[superseded.MemoryID().String()] = superseded
	s.byID[replacement.MemoryID().String()] = replacement
	return s.saveErr
}

func (s *memoryRepositoryStub) FindByID(_ context.Context, memoryID domtypes.MemoryID) (domtypes.Optional[*model.Memory], error) {
	if s.findErr != nil {
		return domtypes.None[*model.Memory](), s.findErr
	}
	if s.byID == nil {
		return domtypes.None[*model.Memory](), nil
	}
	memory, ok := s.byID[memoryID.String()]
	if !ok {
		return domtypes.None[*model.Memory](), nil
	}
	return domtypes.Some(memory), nil
}

type memoryQueryStub struct {
	listResult   []apptypes.MemorySummary
	listErr      error
	searchResult []apptypes.MemorySummary
	searchErr    error
	details      apptypes.MemoryDetails
	detailsErr   error
}

func (s *memoryQueryStub) List(_ context.Context, _ apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	return s.listResult, s.listErr
}

func (s *memoryQueryStub) Search(_ context.Context, _ apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return s.searchResult, s.searchErr
}

func (s *memoryQueryStub) GetDetails(_ context.Context, _ domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return s.details, s.detailsErr
}

func TestMemoryUsecase_Remember(t *testing.T) {
	t.Parallel()

	repo := &memoryRepositoryStub{}
	sut := usecase.NewMemoryUsecase(repo, nil, nil)

	scope := domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary"))
	evidenceRef := mustEvidenceRef(t, domtypes.EvidenceRefKindURL, "https://example.com?token=secret-token")
	artifactRef := mustArtifactRef(t, domtypes.ArtifactRefKindIssue, "#454")

	details, err := sut.Remember(
		context.Background(),
		domtypes.MemoryTypeDecision,
		scope,
		`keep {"api_key":"super-secret"} out of releases`,
		domtypes.None[domtypes.Confidence](),
		domtypes.MemorySource(""),
		[]domtypes.EvidenceRef{evidenceRef},
		[]domtypes.ArtifactRef{artifactRef},
	)
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if len(repo.saveCalls) != 1 {
		t.Fatalf("Save() call count = %d, want 1", len(repo.saveCalls))
	}

	saved := repo.saveCalls[0]
	if saved.Status() != domtypes.MemoryStatusAccepted {
		t.Fatalf("Status() = %s, want accepted", saved.Status())
	}
	if saved.Confidence() != domtypes.ConfidenceVerified {
		t.Fatalf("Confidence() = %s, want verified", saved.Confidence())
	}
	if saved.Source() != domtypes.MemorySourceManual {
		t.Fatalf("Source() = %s, want manual", saved.Source())
	}
	if !strings.Contains(saved.Fact(), "[REDACTED]") {
		t.Fatalf("Fact() = %q, want redacted placeholder", saved.Fact())
	}
	if got := saved.EvidenceRefs()[0].Value(); !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("EvidenceRef.Value() = %q, want redacted placeholder", got)
	}
	if details.Summary().MemoryID().String() == "" {
		t.Fatalf("Summary().MemoryID() is empty")
	}
}

func TestMemoryUsecase_Propose(t *testing.T) {
	t.Parallel()

	repo := &memoryRepositoryStub{}
	sut := usecase.NewMemoryUsecase(repo, nil, nil)

	details, err := sut.Propose(
		context.Background(),
		domtypes.MemoryTypeLesson,
		domtypes.AgentScopeOf(domtypes.Agent("codex")),
		"wait for codex review before merge",
		domtypes.MemorySourceExtracted,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	saved := repo.saveCalls[0]
	if diff := cmp.Diff(domtypes.MemoryStatusCandidate, saved.Status()); diff != "" {
		t.Fatalf("Status() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.ConfidenceLow, saved.Confidence()); diff != "" {
		t.Fatalf("Confidence() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.MemorySourceExtracted, saved.Source()); diff != "" {
		t.Fatalf("Source() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(saved.MemoryID(), details.Summary().MemoryID()); diff != "" {
		t.Fatalf("MemoryID() mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryUsecase_AcceptRequiresEvidence(t *testing.T) {
	t.Parallel()

	memoryID := mustMemoryID(t, "memory-candidate-no-evidence")
	candidate, err := model.NewMemoryCandidate(
		memoryID,
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
		"candidate without evidence",
		domtypes.MemorySourceManual,
		nil,
		nil,
		domtypes.None[domtypes.MemoryID](),
	)
	if err != nil {
		t.Fatalf("NewMemoryCandidate() error = %v", err)
	}

	repo := &memoryRepositoryStub{byID: map[string]*model.Memory{memoryID.String(): candidate}}
	sut := usecase.NewMemoryUsecase(repo, nil, nil)

	if _, err := sut.Accept(context.Background(), memoryID, domtypes.None[domtypes.Confidence]()); err == nil {
		t.Fatal("Accept() error = nil, want error")
	}
}

func TestMemoryUsecase_Accept(t *testing.T) {
	t.Parallel()

	memoryID := mustMemoryID(t, "memory-candidate")
	candidate, err := model.NewMemoryCandidate(
		memoryID,
		domtypes.MemoryTypeConstraint,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
		"candidate with evidence",
		domtypes.MemorySourceManual,
		[]domtypes.EvidenceRef{mustEvidenceRef(t, domtypes.EvidenceRefKindIssue, "#462")},
		nil,
		domtypes.None[domtypes.MemoryID](),
	)
	if err != nil {
		t.Fatalf("NewMemoryCandidate() error = %v", err)
	}

	repo := &memoryRepositoryStub{byID: map[string]*model.Memory{memoryID.String(): candidate}}
	sut := usecase.NewMemoryUsecase(repo, nil, nil)

	details, err := sut.Accept(context.Background(), memoryID, domtypes.Some(domtypes.ConfidenceHigh))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	saved := repo.byID[memoryID.String()]
	if diff := cmp.Diff(domtypes.MemoryStatusAccepted, saved.Status()); diff != "" {
		t.Fatalf("Status() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.ConfidenceHigh, saved.Confidence()); diff != "" {
		t.Fatalf("Confidence() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.MemoryStatusAccepted, details.Summary().Status()); diff != "" {
		t.Fatalf("Summary().Status() mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryUsecase_Supersede(t *testing.T) {
	t.Parallel()

	originalID := mustMemoryID(t, "memory-original")
	original, err := model.NewAcceptedMemory(
		originalID,
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
		"old fact",
		domtypes.ConfidenceVerified,
		domtypes.MemorySourceManual,
		[]domtypes.EvidenceRef{mustEvidenceRef(t, domtypes.EvidenceRefKindIssue, "#453")},
		nil,
		domtypes.None[domtypes.MemoryID](),
	)
	if err != nil {
		t.Fatalf("NewAcceptedMemory() error = %v", err)
	}

	repo := &memoryRepositoryStub{byID: map[string]*model.Memory{originalID.String(): original}}
	sut := usecase.NewMemoryUsecase(repo, nil, nil)

	details, err := sut.Supersede(
		context.Background(),
		originalID,
		domtypes.MemoryType(""),
		nil,
		"new fact",
		domtypes.None[domtypes.Confidence](),
		domtypes.MemorySource(""),
		[]domtypes.EvidenceRef{mustEvidenceRef(t, domtypes.EvidenceRefKindPR, "#467")},
		nil,
	)
	if err != nil {
		t.Fatalf("Supersede() error = %v", err)
	}
	if len(repo.supersessionCalls) != 1 {
		t.Fatalf("SaveSupersession() call count = %d, want 1", len(repo.supersessionCalls))
	}
	if diff := cmp.Diff(domtypes.MemoryStatusSuperseded, repo.byID[originalID.String()].Status()); diff != "" {
		t.Fatalf("original status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(originalID.String(), mustMemoryIDString(t, details.Summary().Supersedes())); diff != "" {
		t.Fatalf("Supersedes() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.MemoryStatusAccepted, details.Summary().Status()); diff != "" {
		t.Fatalf("replacement status mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryUsecase_Expire(t *testing.T) {
	t.Parallel()

	memoryID := mustMemoryID(t, "memory-expire")
	memory, err := model.NewAcceptedMemory(
		memoryID,
		domtypes.MemoryTypeConstraint,
		domtypes.AgentScopeOf(domtypes.Agent("codex")),
		"expires later",
		domtypes.ConfidenceVerified,
		domtypes.MemorySourceManual,
		[]domtypes.EvidenceRef{mustEvidenceRef(t, domtypes.EvidenceRefKindIssue, "#462")},
		nil,
		domtypes.None[domtypes.MemoryID](),
	)
	if err != nil {
		t.Fatalf("NewAcceptedMemory() error = %v", err)
	}
	when := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

	repo := &memoryRepositoryStub{byID: map[string]*model.Memory{memoryID.String(): memory}}
	sut := usecase.NewMemoryUsecase(repo, nil, nil)

	details, err := sut.Expire(context.Background(), memoryID, domtypes.Some(when))
	if err != nil {
		t.Fatalf("Expire() error = %v", err)
	}
	if diff := cmp.Diff(domtypes.MemoryStatusExpired, repo.byID[memoryID.String()].Status()); diff != "" {
		t.Fatalf("Status() mismatch (-want +got):\n%s", diff)
	}
	expiresAt, ok := details.Summary().ExpiresAt().Value()
	if !ok {
		t.Fatal("ExpiresAt() missing, want value")
	}
	if diff := cmp.Diff(when, expiresAt); diff != "" {
		t.Fatalf("ExpiresAt() mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryUsecase_SetValidity(t *testing.T) {
	t.Parallel()

	makeMemory := func(t *testing.T) *model.Memory {
		t.Helper()
		m, err := model.NewAcceptedMemory(
			mustMemoryID(t, "memory-validity"),
			domtypes.MemoryTypeDecision,
			domtypes.AgentScopeOf(domtypes.Agent("codex")),
			"decision with a lifetime",
			domtypes.ConfidenceVerified,
			domtypes.MemorySourceManual,
			[]domtypes.EvidenceRef{mustEvidenceRef(t, domtypes.EvidenceRefKindIssue, "#500")},
			nil,
			domtypes.None[domtypes.MemoryID](),
		)
		if err != nil {
			t.Fatalf("NewAcceptedMemory() error = %v", err)
		}
		return m
	}

	t.Run("sets both bounds", func(t *testing.T) {
		t.Parallel()
		memory := makeMemory(t)
		repo := &memoryRepositoryStub{byID: map[string]*model.Memory{memory.MemoryID().String(): memory}}
		sut := usecase.NewMemoryUsecase(repo, nil, nil)
		from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		details, err := sut.SetValidity(
			context.Background(),
			memory.MemoryID(),
			domtypes.Some(from),
			domtypes.Some(to),
			false,
		)
		if err != nil {
			t.Fatalf("SetValidity() error = %v", err)
		}
		if diff := cmp.Diff(from, details.Summary().ValidFrom()); diff != "" {
			t.Fatalf("ValidFrom() mismatch (-want +got):\n%s", diff)
		}
		gotTo, ok := details.Summary().ValidTo().Value()
		if !ok || !gotTo.Equal(to) {
			t.Fatalf("ValidTo() = %v/%v, want %v", gotTo, ok, to)
		}
	})

	t.Run("rejects reversed window when only validFrom is supplied and existing validTo precedes it", func(t *testing.T) {
		t.Parallel()
		memory := makeMemory(t)
		memory.SetValidity(domtypes.None[time.Time](), domtypes.Some(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)))
		repo := &memoryRepositoryStub{byID: map[string]*model.Memory{memory.MemoryID().String(): memory}}
		sut := usecase.NewMemoryUsecase(repo, nil, nil)
		later := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		if _, err := sut.SetValidity(
			context.Background(),
			memory.MemoryID(),
			domtypes.Some(later),
			domtypes.None[time.Time](),
			false,
		); err == nil {
			t.Fatalf("SetValidity() error = nil; want reversed-window error")
		}
	})

	t.Run("rejects reversed window when only validTo is supplied and new validTo precedes existing validFrom", func(t *testing.T) {
		t.Parallel()
		memory := makeMemory(t)
		memory.SetValidity(domtypes.Some(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)), domtypes.None[time.Time]())
		repo := &memoryRepositoryStub{byID: map[string]*model.Memory{memory.MemoryID().String(): memory}}
		sut := usecase.NewMemoryUsecase(repo, nil, nil)
		earlier := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
		if _, err := sut.SetValidity(
			context.Background(),
			memory.MemoryID(),
			domtypes.None[time.Time](),
			domtypes.Some(earlier),
			false,
		); err == nil {
			t.Fatalf("SetValidity() error = nil; want reversed-window error")
		}
	})

	t.Run("clearValidTo returns memory to open-ended", func(t *testing.T) {
		t.Parallel()
		memory := makeMemory(t)
		memory.SetValidity(domtypes.None[time.Time](), domtypes.Some(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)))
		repo := &memoryRepositoryStub{byID: map[string]*model.Memory{memory.MemoryID().String(): memory}}
		sut := usecase.NewMemoryUsecase(repo, nil, nil)
		details, err := sut.SetValidity(
			context.Background(),
			memory.MemoryID(),
			domtypes.None[time.Time](),
			domtypes.None[time.Time](),
			true,
		)
		if err != nil {
			t.Fatalf("SetValidity() error = %v", err)
		}
		if _, ok := details.Summary().ValidTo().Value(); ok {
			t.Fatalf("ValidTo() = present; want cleared")
		}
	})

	t.Run("rejects clearValidTo combined with an explicit validTo", func(t *testing.T) {
		t.Parallel()
		memory := makeMemory(t)
		repo := &memoryRepositoryStub{byID: map[string]*model.Memory{memory.MemoryID().String(): memory}}
		sut := usecase.NewMemoryUsecase(repo, nil, nil)
		to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		if _, err := sut.SetValidity(
			context.Background(),
			memory.MemoryID(),
			domtypes.None[time.Time](),
			domtypes.Some(to),
			true,
		); err == nil {
			t.Fatalf("SetValidity() error = nil; want mutual-exclusion error")
		}
	})
}

func TestMemoryUsecase_Show(t *testing.T) {
	t.Parallel()

	memory := mustAcceptedMemory(t, "memory-show", "shown fact")
	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		t.Fatalf("MemoryDetailsFrom() error = %v", err)
	}

	sut := usecase.NewMemoryUsecase(nil, &memoryQueryStub{details: details}, nil)
	got, err := sut.Show(context.Background(), memory.MemoryID())
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if diff := cmp.Diff(details.Summary().MemoryID(), got.Summary().MemoryID()); diff != "" {
		t.Fatalf("MemoryID() mismatch (-want +got):\n%s", diff)
	}
}

func mustMemoryID(t *testing.T, value string) domtypes.MemoryID {
	t.Helper()

	memoryID, err := domtypes.MemoryIDOf(value)
	if err != nil {
		t.Fatalf("MemoryIDOf() error = %v", err)
	}
	return memoryID
}

func mustEvidenceRef(t *testing.T, kind domtypes.EvidenceRefKind, value string) domtypes.EvidenceRef {
	t.Helper()

	ref, err := domtypes.EvidenceRefOf(kind, value)
	if err != nil {
		t.Fatalf("EvidenceRefOf() error = %v", err)
	}
	return ref
}

func mustArtifactRef(t *testing.T, kind domtypes.ArtifactRefKind, value string) domtypes.ArtifactRef {
	t.Helper()

	ref, err := domtypes.ArtifactRefOf(kind, value)
	if err != nil {
		t.Fatalf("ArtifactRefOf() error = %v", err)
	}
	return ref
}

func mustAcceptedMemory(t *testing.T, memoryIDValue string, fact string) *model.Memory {
	t.Helper()

	memory, err := model.NewAcceptedMemory(
		mustMemoryID(t, memoryIDValue),
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
		fact,
		domtypes.ConfidenceVerified,
		domtypes.MemorySourceManual,
		[]domtypes.EvidenceRef{mustEvidenceRef(t, domtypes.EvidenceRefKindIssue, "#454")},
		[]domtypes.ArtifactRef{mustArtifactRef(t, domtypes.ArtifactRefKindPR, "#467")},
		domtypes.None[domtypes.MemoryID](),
	)
	if err != nil {
		t.Fatalf("NewAcceptedMemory() error = %v", err)
	}
	return memory
}

func mustMemoryIDString(t *testing.T, value domtypes.Optional[domtypes.MemoryID]) string {
	t.Helper()

	memoryID, ok := value.Value()
	if !ok {
		t.Fatal("Optional.Value() = empty, want value")
	}
	return memoryID.String()
}
