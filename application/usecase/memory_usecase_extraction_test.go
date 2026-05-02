package usecase_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryExtractionMemoryUsecaseStub struct {
	listResult    []apptypes.MemorySummary
	listErr       error
	listCalls     []apptypes.MemoryListCriteria
	proposeResult []apptypes.MemoryDetails
	proposeErr    error
	proposeCalls  []memoryExtractionProposeCall
}

func (s *memoryExtractionMemoryUsecaseStub) Save(_ context.Context, memory *model.Memory) error {
	s.proposeCalls = append(s.proposeCalls, memoryExtractionProposeCall{
		memoryType:   memory.MemoryType(),
		scope:        memory.Scope(),
		fact:         memory.Fact(),
		source:       memory.Source(),
		evidenceRefs: append([]domtypes.EvidenceRef(nil), memory.EvidenceRefs()...),
		artifactRefs: append([]domtypes.ArtifactRef(nil), memory.ArtifactRefs()...),
	})
	return s.proposeErr
}

func (s *memoryExtractionMemoryUsecaseStub) SaveSupersession(context.Context, *model.Memory, *model.Memory) error {
	return nil
}

func (s *memoryExtractionMemoryUsecaseStub) FindByID(context.Context, domtypes.MemoryID) (domtypes.Optional[*model.Memory], error) {
	return domtypes.None[*model.Memory](), nil
}

type memoryExtractionProposeCall struct {
	memoryType   domtypes.MemoryType
	scope        domtypes.MemoryScope
	fact         string
	source       domtypes.MemorySource
	evidenceRefs []domtypes.EvidenceRef
	artifactRefs []domtypes.ArtifactRef
}

func (s *memoryExtractionMemoryUsecaseStub) Remember(context.Context, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.Optional[domtypes.Confidence], domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) Propose(_ context.Context, memoryType domtypes.MemoryType, scope domtypes.MemoryScope, fact string, source domtypes.MemorySource, evidenceRefs []domtypes.EvidenceRef, artifactRefs []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	s.proposeCalls = append(s.proposeCalls, memoryExtractionProposeCall{
		memoryType:   memoryType,
		scope:        scope,
		fact:         fact,
		source:       source,
		evidenceRefs: append([]domtypes.EvidenceRef(nil), evidenceRefs...),
		artifactRefs: append([]domtypes.ArtifactRef(nil), artifactRefs...),
	})
	if s.proposeErr != nil {
		return apptypes.MemoryDetails{}, s.proposeErr
	}
	if len(s.proposeResult) >= len(s.proposeCalls) {
		return s.proposeResult[len(s.proposeCalls)-1], nil
	}
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) Accept(context.Context, domtypes.MemoryID, domtypes.Optional[domtypes.Confidence]) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) Reject(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) Supersede(context.Context, domtypes.MemoryID, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.Optional[domtypes.Confidence], domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef, domtypes.Optional[time.Time], domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) Expire(context.Context, domtypes.MemoryID, domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) SetValidity(context.Context, domtypes.MemoryID, domtypes.Optional[time.Time], domtypes.Optional[time.Time], bool) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.listCalls = append(s.listCalls, criteria)
	if s.listErr != nil {
		return nil, s.listErr
	}
	start := criteria.Offset()
	if start >= len(s.listResult) {
		return nil, nil
	}
	end := start + criteria.Limit()
	if end > len(s.listResult) {
		end = len(s.listResult)
	}
	return append([]apptypes.MemorySummary(nil), s.listResult[start:end]...), nil
}

func (s *memoryExtractionMemoryUsecaseStub) Search(context.Context, apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}

func (s *memoryExtractionMemoryUsecaseStub) Show(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) GetDetails(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func TestMemoryUsecase_Extract(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"active",
		12,
		4,
		[]string{"claude"},
		"",
		"Decision: Use ContextUsecase for handoff output",
		domtypes.SessionID(""),
	)
	promptEvent := mustExtractionEvent(t,
		"event-prompt",
		domtypes.EventKindPrompt,
		"Please answer in Japanese and never merge before Codex review.",
	)
	noteEvent := mustExtractionEvent(t,
		"event-note",
		domtypes.EventKindNote,
		"Constraint: Keep get_context as raw event retrieval.",
	)
	compactEvent := mustExtractionEvent(t,
		"event-compact",
		domtypes.EventKindCompactSummary,
		"<summary>\nLesson: Extracted candidates should remain review-only.\nArtifact: docs/cli/README.md\n</summary>",
	)

	details1 := mustMemoryDetailsFromSummary(t, "memory-candidate-1", domtypes.MemoryTypeDecision, "Use ContextUsecase for handoff output")
	details2 := mustMemoryDetailsFromSummary(t, "memory-candidate-2", domtypes.MemoryTypePreference, "Please answer in Japanese and never merge before Codex review.")
	details3 := mustMemoryDetailsFromSummary(t, "memory-candidate-3", domtypes.MemoryTypeConstraint, "Keep get_context as raw event retrieval.")
	details4 := mustMemoryDetailsFromSummary(t, "memory-candidate-4", domtypes.MemoryTypeLesson, "Extracted candidates should remain review-only.")
	details5 := mustMemoryDetailsFromSummary(t, "memory-candidate-5", domtypes.MemoryTypeArtifact, "docs/cli/README.md")

	memoryUsecase := &memoryExtractionMemoryUsecaseStub{
		proposeResult: []apptypes.MemoryDetails{details1, details2, details3, details4, details5},
	}

	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt:         {promptEvent},
			domtypes.EventKindNote:           {noteEvent},
			domtypes.EventKindCompactSummary: {compactEvent},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-1")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(3).
			CandidateLimit(10).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len(Extract()) = %d, want 5", len(got))
	}

	gotTypes := make([]domtypes.MemoryType, 0, len(memoryUsecase.proposeCalls))
	gotFacts := make([]string, 0, len(memoryUsecase.proposeCalls))
	for _, call := range memoryUsecase.proposeCalls {
		gotTypes = append(gotTypes, call.memoryType)
		gotFacts = append(gotFacts, call.fact)
		if diff := cmp.Diff(domtypes.MemorySourceExtracted, call.source); diff != "" {
			t.Fatalf("source mismatch (-want +got):\n%s", diff)
		}
		if call.scope == nil || call.scope.Kind() != domtypes.MemoryScopeKindWorkspace || call.scope.Key() != "github.com/duck8823/traceary" {
			t.Fatalf("scope = %v, want workspace scope", call.scope)
		}
		if len(call.evidenceRefs) == 0 {
			t.Fatalf("evidenceRefs = empty, want session/event evidence")
		}
	}
	if diff := cmp.Diff(
		[]domtypes.MemoryType{
			domtypes.MemoryTypeDecision,
			domtypes.MemoryTypePreference,
			domtypes.MemoryTypeConstraint,
			domtypes.MemoryTypeLesson,
			domtypes.MemoryTypeArtifact,
		},
		gotTypes,
	); diff != "" {
		t.Fatalf("memory types mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(
		[]string{
			"Use ContextUsecase for handoff output",
			"Please answer in Japanese and never merge before Codex review.",
			"Keep get_context as raw event retrieval.",
			"Extracted candidates should remain review-only.",
			"docs/cli/README.md",
		},
		gotFacts,
	); diff != "" {
		t.Fatalf("facts mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryUsecase_Extract_IncludesTranscriptEvents(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-transcript"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"active",
		1,
		0,
		[]string{"claude"},
		"",
		"",
		domtypes.SessionID(""),
	)
	transcriptEvent := mustExtractionEvent(t,
		"event-transcript",
		domtypes.EventKindTranscript,
		"Decision: adopt the application/redaction leaf package.",
	)

	details := mustMemoryDetailsFromSummary(t, "memory-transcript-1", domtypes.MemoryTypeDecision, "adopt the application/redaction leaf package.")

	memoryUsecase := &memoryExtractionMemoryUsecaseStub{
		proposeResult: []apptypes.MemoryDetails{details},
	}

	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindTranscript: {transcriptEvent},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-transcript")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(1).
			CandidateLimit(5).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Extract()) = %d, want 1 transcript-derived candidate", len(got))
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1", len(memoryUsecase.proposeCalls))
	}
	if memoryUsecase.proposeCalls[0].memoryType != domtypes.MemoryTypeDecision {
		t.Errorf("memoryType = %v, want Decision", memoryUsecase.proposeCalls[0].memoryType)
	}
}

func TestMemoryUsecase_Extract_ClaudeJapaneseSummarySignals(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-claude-ja"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		3,
		0,
		[]string{"claude"},
		"",
		"",
		domtypes.SessionID(""),
	)
	compactEvent := mustExtractionEvent(t,
		"event-claude-summary",
		domtypes.EventKindCompactSummary,
		strings.Join([]string{
			"<summary>",
			"835 は tech-debt ではない。設計/API 判断が必要な新規タスクとして扱う。",
			"次回 Claude Code restart で stale MCP server cache は解消する。",
			"v0.11.0 release PR #840 と Homebrew PR #841 は merge 済み。",
			"</summary>",
		}, "\n"),
	)

	details1 := mustMemoryDetailsFromSummary(t, "memory-claude-ja-1", domtypes.MemoryTypeDecision, "835 は tech-debt ではない。設計/API 判断が必要な新規タスクとして扱う。")
	details2 := mustMemoryDetailsFromSummary(t, "memory-claude-ja-2", domtypes.MemoryTypeLesson, "次回 Claude Code restart で stale MCP server cache は解消する。")
	details3 := mustMemoryDetailsFromSummary(t, "memory-claude-ja-3", domtypes.MemoryTypeDecision, "v0.11.0 release PR #840 と Homebrew PR #841 は merge 済み。")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{
		proposeResult: []apptypes.MemoryDetails{details1, details2, details3},
	}

	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindCompactSummary: {compactEvent},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-claude-ja")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(1).
			CandidateLimit(10).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(Extract()) = %d, want 3", len(got))
	}

	gotTypes := make([]domtypes.MemoryType, 0, len(memoryUsecase.proposeCalls))
	gotFacts := make([]string, 0, len(memoryUsecase.proposeCalls))
	for _, call := range memoryUsecase.proposeCalls {
		gotTypes = append(gotTypes, call.memoryType)
		gotFacts = append(gotFacts, call.fact)
	}
	if diff := cmp.Diff([]domtypes.MemoryType{domtypes.MemoryTypeDecision, domtypes.MemoryTypeLesson, domtypes.MemoryTypeDecision}, gotTypes); diff != "" {
		t.Fatalf("memory types mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{
		"835 は tech-debt ではない。設計/API 判断が必要な新規タスクとして扱う。",
		"次回 Claude Code restart で stale MCP server cache は解消する。",
		"v0.11.0 release PR #840 と Homebrew PR #841 は merge 済み。",
	}, gotFacts); diff != "" {
		t.Fatalf("facts mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryUsecase_Extract_ScoresWeakSignalsAsHidden(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-score"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		2,
		0,
		[]string{"claude"},
		"",
		"",
		domtypes.SessionID(""),
	)
	weakPrompt := mustExtractionEvent(t, "event-weak", domtypes.EventKindPrompt, "must fix")
	structuredPrompt := mustExtractionEvent(t, "event-structured", domtypes.EventKindPrompt, "Decision: Ship")

	details1 := mustMemoryDetailsFromSummary(t, "memory-score-1", domtypes.MemoryTypeConstraint, "must fix")
	details2 := mustMemoryDetailsFromSummary(t, "memory-score-2", domtypes.MemoryTypeDecision, "Ship")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{
		proposeResult: []apptypes.MemoryDetails{details1, details2},
	}

	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt: {weakPrompt, structuredPrompt},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-score")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(2).
			CandidateLimit(10).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 2 {
		t.Fatalf("proposeCalls = %d, want 2", len(memoryUsecase.proposeCalls))
	}
	if got := memoryUsecase.proposeCalls[0].source; got != domtypes.MemorySourceExtractedHidden {
		t.Fatalf("weak source = %q, want extracted-hidden", got)
	}
	if got := memoryUsecase.proposeCalls[1].source; got != domtypes.MemorySourceExtracted {
		t.Fatalf("structured source = %q, want extracted", got)
	}
}

func TestMemoryUsecase_Extract_DeduplicatesByBestSignalScore(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-best-score"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		2,
		0,
		[]string{"claude"},
		"",
		"",
		domtypes.SessionID(""),
	)
	weakPrompt := mustExtractionEvent(t, "event-weak-duplicate", domtypes.EventKindPrompt, "must fix")
	structuredPrompt := mustExtractionEvent(t, "event-structured-duplicate", domtypes.EventKindPrompt, "Constraint: must fix")

	details := mustMemoryDetailsFromSummary(t, "memory-best-score-1", domtypes.MemoryTypeConstraint, "must fix")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{
		proposeResult: []apptypes.MemoryDetails{details},
	}

	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt: {weakPrompt, structuredPrompt},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-best-score")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(2).
			CandidateLimit(10).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Extract()) = %d, want 1 deduped candidate", len(got))
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1", len(memoryUsecase.proposeCalls))
	}
	call := memoryUsecase.proposeCalls[0]
	if call.fact != "must fix" {
		t.Fatalf("fact = %q, want deduped structured fact", call.fact)
	}
	if call.source != domtypes.MemorySourceExtracted {
		t.Fatalf("source = %q, want high-score extracted source", call.source)
	}
}

func TestMemoryUsecase_Extract_DeduplicatesExistingAndGracefullyHandlesMissingPrompts(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		4,
		0,
		[]string{"codex"},
		"",
		"Lesson: Wait for explicit review completion before merge",
		domtypes.SessionID(""),
	)
	existingSummary, err := apptypes.MemorySummaryOf(
		mustMemoryID(t, "memory-existing"),
		domtypes.MemoryTypeLesson,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
		"Wait for explicit review completion before merge",
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceVerified,
		domtypes.MemorySourceExtracted,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Now(),
		domtypes.None[time.Time](),
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}

	memoryUsecase := &memoryExtractionMemoryUsecaseStub{
		listResult: []apptypes.MemorySummary{existingSummary},
	}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-1")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(0).
			CandidateLimit(5).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(Extract()) = %d, want 0", len(got))
	}
	if len(memoryUsecase.proposeCalls) != 0 {
		t.Fatalf("Propose() call count = %d, want 0", len(memoryUsecase.proposeCalls))
	}
}

func TestMemoryUsecase_Extract_PaginatesExistingMemoryDedupe(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		4,
		0,
		[]string{"codex"},
		"",
		"Decision: Keep get_context raw",
		domtypes.SessionID(""),
	)

	listResult := make([]apptypes.MemorySummary, 0, 201)
	for idx := 0; idx < 200; idx++ {
		summary, err := apptypes.MemorySummaryOf(
			mustMemoryID(t, fmt.Sprintf("memory-existing-%03d", idx)),
			domtypes.MemoryTypeLesson,
			domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
			fmt.Sprintf("Existing lesson %03d", idx),
			domtypes.MemoryStatusCandidate,
			domtypes.ConfidenceVerified,
			domtypes.MemorySourceExtracted,
			domtypes.None[domtypes.MemoryID](),
			domtypes.None[time.Time](),
			time.Now(),
			domtypes.None[time.Time](),
			time.Now(),
			time.Now(),
		)
		if err != nil {
			t.Fatalf("MemorySummaryOf() error = %v", err)
		}
		listResult = append(listResult, summary)
	}
	duplicateSummary, err := apptypes.MemorySummaryOf(
		mustMemoryID(t, "memory-existing-duplicate"),
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
		"Keep get_context raw",
		domtypes.MemoryStatusAccepted,
		domtypes.ConfidenceVerified,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Now(),
		domtypes.None[time.Time](),
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	listResult = append(listResult, duplicateSummary)

	memoryUsecase := &memoryExtractionMemoryUsecaseStub{listResult: listResult}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-1")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(0).
			CandidateLimit(5).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(Extract()) = %d, want 0", len(got))
	}
	if len(memoryUsecase.proposeCalls) != 0 {
		t.Fatalf("Propose() call count = %d, want 0", len(memoryUsecase.proposeCalls))
	}
	if len(memoryUsecase.listCalls) != 2 {
		t.Fatalf("List() call count = %d, want 2", len(memoryUsecase.listCalls))
	}
	if got := memoryUsecase.listCalls[0].Offset(); got != 0 {
		t.Fatalf("first List().Offset() = %d, want 0", got)
	}
	if got := memoryUsecase.listCalls[1].Offset(); got != 200 {
		t.Fatalf("second List().Offset() = %d, want 200", got)
	}
}

func TestMemoryUsecase_Extract_DeduplicatesSanitizedFacts(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		4,
		0,
		[]string{"codex"},
		"",
		"",
		domtypes.SessionID(""),
	)
	promptEvent := mustExtractionEvent(
		t,
		"event-prompt",
		domtypes.EventKindPrompt,
		"Please keep password=secret-one out of generated examples.\nPlease keep password=secret-two out of generated examples.",
	)

	details := mustMemoryDetailsFromSummary(
		t,
		"memory-candidate-sanitized",
		domtypes.MemoryTypePreference,
		"Please keep password=[REDACTED] out of generated examples.",
	)
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{
		proposeResult: []apptypes.MemoryDetails{details},
	}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt: {promptEvent},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-1")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(5).
			CandidateLimit(10).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Extract()) = %d, want 1", len(got))
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("Propose() call count = %d, want 1", len(memoryUsecase.proposeCalls))
	}
}

func TestMemoryUsecase_Extract_DeduplicatesExistingFactsAfterSanitization(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		4,
		0,
		[]string{"codex"},
		"",
		"",
		domtypes.SessionID(""),
	)

	testCases := []struct {
		name                string
		extraRedactPatterns []string
		existingFact        string
		promptFact          string
	}{
		{
			// Built-in redactors already catch `password=<value>`, so this
			// sub-case exercises the dedupe path driven by the default
			// sanitizer — the original coverage.
			name:                "built-in password redactor",
			extraRedactPatterns: nil,
			existingFact:        "Please keep password=secret-one out of generated examples.",
			promptFact:          "Please keep password=secret-two out of generated examples.",
		},
		{
			// This sub-case exercises the core guarantee of the dedupe fix:
			// two facts whose raw values differ must collapse to the same key
			// *after* a caller-supplied redaction pattern normalizes them.
			// Both facts start with "Please" so inferMemoryTypeFromText
			// actually produces a MemoryTypePreference candidate — an earlier
			// iteration used "Remember ..." which matches no heuristic
			// prefix, making the extractor emit nothing and the test pass
			// for the wrong reason (caught by the Codex verifier). The
			// `internalCode=` token is outside the built-in redactor
			// alternation (password|secret|token|...), so only the
			// caller-supplied pattern below can normalize the two values to
			// the same key.
			name:                "custom extra redact pattern",
			extraRedactPatterns: []string{`internalCode=\S+`},
			existingFact:        "Please keep internalCode=alpha-one out of rendered templates.",
			promptFact:          "Please keep internalCode=beta-two out of rendered templates.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			promptEvent := mustExtractionEvent(
				t,
				"event-prompt",
				domtypes.EventKindPrompt,
				tc.promptFact,
			)

			existingSummary, err := apptypes.MemorySummaryOf(
				mustMemoryID(t, "memory-existing-sanitized"),
				domtypes.MemoryTypePreference,
				domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
				tc.existingFact,
				domtypes.MemoryStatusCandidate,
				domtypes.ConfidenceVerified,
				domtypes.MemorySourceExtracted,
				domtypes.None[domtypes.MemoryID](),
				domtypes.None[time.Time](),
				time.Now(),
				domtypes.None[time.Time](),
				time.Now(),
				time.Now(),
			)
			if err != nil {
				t.Fatalf("MemorySummaryOf() error = %v", err)
			}

			memoryUsecase := &memoryExtractionMemoryUsecaseStub{
				listResult: []apptypes.MemorySummary{existingSummary},
			}
			sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
			eventQuery := &eventQueryServiceStub{
				listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
					domtypes.EventKindPrompt: {promptEvent},
				},
			}
			sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, tc.extraRedactPatterns, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

			got, err := sut.Extract(
				context.Background(),
				apptypes.NewMemoryExtractionCriteriaBuilder().
					SessionID(domtypes.SessionID("session-1")).
					Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
					EventLimit(5).
					CandidateLimit(10).
					Build(),
			)
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("len(Extract()) = %d, want 0", len(got))
			}
			if len(memoryUsecase.proposeCalls) != 0 {
				t.Fatalf("Propose() call count = %d, want 0", len(memoryUsecase.proposeCalls))
			}
		})
	}
}

func mustExtractionEvent(t *testing.T, eventID string, kind domtypes.EventKind, body string) *model.Event {
	t.Helper()

	event, err := model.NewEvent(
		domtypes.EventID(eventID),
		kind,
		domtypes.Client("cli"),
		domtypes.Agent("claude"),
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		body,
	)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}

func mustExtractionEventAt(t *testing.T, eventID string, kind domtypes.EventKind, body string, createdAt time.Time) *model.Event {
	t.Helper()

	return model.EventOf(
		domtypes.EventID(eventID),
		kind,
		domtypes.Client("cli"),
		domtypes.Agent("claude"),
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		body,
		createdAt,
	)
}

func mustMemoryDetailsFromSummary(t *testing.T, memoryID string, memoryType domtypes.MemoryType, fact string) apptypes.MemoryDetails {
	t.Helper()

	summary, err := apptypes.MemorySummaryOf(
		mustMemoryID(t, memoryID),
		memoryType,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
		fact,
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceVerified,
		domtypes.MemorySourceExtracted,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Now(),
		domtypes.None[time.Time](),
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	return apptypes.MemoryDetailsOf(summary, []domtypes.EvidenceRef{mustEvidenceRef(t, domtypes.EvidenceRefKindSession, "session-1")}, nil)
}

func TestMemoryUsecase_Extract_ExplicitDurableMemoryIntentFromCompactSummary(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-durable-intent"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		3,
		0,
		[]string{"codex"},
		"",
		"",
		domtypes.SessionID(""),
	)
	compactEvent := mustExtractionEvent(t,
		"event-durable-intent",
		domtypes.EventKindCompactSummary,
		"Durable Memory: When dogfooding v0.11.1, verify command audit show/context include INPUT and OUTPUT fields before closing the validation session. Evidence: dogfood session session-1.",
	)
	details := mustMemoryDetailsFromSummary(t, "memory-durable-intent", domtypes.MemoryTypeLesson, "When dogfooding v0.11.1, verify command audit show/context include INPUT and OUTPUT fields before closing the validation session. Evidence: dogfood session session-1.")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindCompactSummary: {compactEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-durable-intent")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Extract()) = %d, want 1", len(got))
	}
	call := memoryUsecase.proposeCalls[0]
	if call.memoryType != domtypes.MemoryTypeLesson {
		t.Fatalf("memoryType = %q, want lesson fallback", call.memoryType)
	}
	if call.source != domtypes.MemorySourceExtracted {
		t.Fatalf("source = %q, want visible extracted", call.source)
	}
	if strings.HasPrefix(call.fact, "Durable Memory:") {
		t.Fatalf("fact kept explicit label: %q", call.fact)
	}
}

func TestMemoryUsecase_Extract_JapaneseExplicitMemoryIntent(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-ja-intent"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 2, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-ja-intent", domtypes.EventKindPrompt, "覚えておいて: Codex review は数分かかることがあるので、PR review/check 状態をポーリングして待つ。")
	details := mustMemoryDetailsFromSummary(t, "memory-ja-intent", domtypes.MemoryTypeLesson, "Codex review は数分かかることがあるので、PR review/check 状態をポーリングして待つ。")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-ja-intent")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1", len(memoryUsecase.proposeCalls))
	}
	if got := memoryUsecase.proposeCalls[0].source; got != domtypes.MemorySourceRememberIntent {
		t.Fatalf("source = %q, want remember-intent", got)
	}
}

func TestMemoryUsecase_Extract_RememberIntentInlineFactsUseRememberSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     string
		wantFact string
	}{
		{
			name:     "english trigger before fact without colon",
			body:     "remember this Codex review can take a few minutes before comments appear.",
			wantFact: "Codex review can take a few minutes before comments appear",
		},
		{
			// Verifies the inline trigger scan stays byte-safe when the
			// preceding text contains a Unicode character whose lowercase
			// form expands ("İ" → "i̇"). The imperative gate still requires
			// a sentence boundary before the trigger, so the regression is
			// kept inside an imperative context.
			name:     "english trigger after unicode case-folding expansion",
			body:     "İ. Remember this use Japanese responses",
			wantFact: "use Japanese responses",
		},
		{
			name:     "english trigger after fact",
			body:     "Codex review can take a few minutes before comments appear, remember this.",
			wantFact: "Codex review can take a few minutes before comments appear",
		},
		{
			name:     "japanese trigger before fact",
			body:     "覚えておいて: Codex review は数分かかることがある。",
			wantFact: "Codex review は数分かかることがある。",
		},
		{
			name:     "japanese trigger after fact",
			body:     "Codex review は数分かかることがあるので覚えておいて",
			wantFact: "Codex review は数分かかることがあるので",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := apptypes.SessionSummaryOf(domtypes.SessionID("session-remember-inline"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
			promptEvent := mustExtractionEvent(t, "event-remember-inline", domtypes.EventKindPrompt, tc.body)
			details := mustMemoryDetailsFromSummary(t, "memory-remember-inline", domtypes.MemoryTypeLesson, tc.wantFact)
			memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
			sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
			eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
			sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

			_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-remember-inline")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if len(memoryUsecase.proposeCalls) != 1 {
				t.Fatalf("proposeCalls = %d, want 1", len(memoryUsecase.proposeCalls))
			}
			call := memoryUsecase.proposeCalls[0]
			if call.fact != tc.wantFact {
				t.Fatalf("fact = %q, want %q", call.fact, tc.wantFact)
			}
			if call.source != domtypes.MemorySourceRememberIntent {
				t.Fatalf("source = %q, want remember-intent", call.source)
			}
		})
	}
}

func TestMemoryUsecase_Extract_ShortRememberIntentUsesAdjacentContextEvidence(t *testing.T) {
	t.Parallel()

	now := time.Now()
	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-remember-context"), domtypes.Workspace("github.com/duck8823/traceary"), now.Add(-time.Hour), domtypes.None[time.Time](), "ended", 2, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	contextEvent := mustExtractionEventAt(t, "event-context", domtypes.EventKindPrompt, "Please answer in Japanese for this repository.", now)
	rememberEvent := mustExtractionEventAt(t, "event-remember-short", domtypes.EventKindPrompt, "覚えておいてね", now.Add(time.Second))
	details := mustMemoryDetailsFromSummary(t, "memory-remember-context", domtypes.MemoryTypePreference, "Please answer in Japanese for this repository.")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt: {rememberEvent, contextEvent},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-remember-context")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(2).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1 adjacent-context candidate", len(memoryUsecase.proposeCalls))
	}
	call := memoryUsecase.proposeCalls[0]
	if call.fact != "Please answer in Japanese for this repository." {
		t.Fatalf("fact = %q, want adjacent context fact", call.fact)
	}
	if call.source != domtypes.MemorySourceRememberIntent {
		t.Fatalf("source = %q, want remember-intent", call.source)
	}
	gotEvidence := make(map[string]bool)
	for _, ref := range call.evidenceRefs {
		if ref.Kind() == domtypes.EvidenceRefKindEvent {
			gotEvidence[ref.Value()] = true
		}
	}
	for _, eventID := range []string{"event-remember-short", "event-context"} {
		if !gotEvidence[eventID] {
			t.Fatalf("evidence refs = %v, want event %s", call.evidenceRefs, eventID)
		}
	}
}

func TestMemoryUsecase_Extract_ShortRememberIntentSkipsNonFactualAdjacentAck(t *testing.T) {
	t.Parallel()

	now := time.Now()
	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-remember-skip-ack"), domtypes.Workspace("github.com/duck8823/traceary"), now.Add(-time.Hour), domtypes.None[time.Time](), "ended", 3, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	contextEvent := mustExtractionEventAt(t, "event-factual-context", domtypes.EventKindPrompt, "Always run go test before merging.", now)
	ackEvent := mustExtractionEventAt(t, "event-ack-context", domtypes.EventKindTranscript, "Sure, got it.", now.Add(time.Second))
	rememberEvent := mustExtractionEventAt(t, "event-remember-after-ack", domtypes.EventKindPrompt, "覚えておいてね", now.Add(2*time.Second))
	details := mustMemoryDetailsFromSummary(t, "memory-remember-skip-ack", domtypes.MemoryTypePreference, "Always run go test before merging.")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt:     {rememberEvent, contextEvent},
			domtypes.EventKindTranscript: {ackEvent},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-remember-skip-ack")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(3).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1 adjacent factual context candidate", len(memoryUsecase.proposeCalls))
	}
	call := memoryUsecase.proposeCalls[0]
	if call.fact != "Always run go test before merging." {
		t.Fatalf("fact = %q, want factual context rather than acknowledgement", call.fact)
	}
	gotEvidence := make(map[string]bool)
	for _, ref := range call.evidenceRefs {
		if ref.Kind() == domtypes.EvidenceRefKindEvent {
			gotEvidence[ref.Value()] = true
		}
	}
	if gotEvidence["event-ack-context"] {
		t.Fatalf("evidence refs = %v, should not include non-factual acknowledgement", call.evidenceRefs)
	}
	for _, eventID := range []string{"event-remember-after-ack", "event-factual-context"} {
		if !gotEvidence[eventID] {
			t.Fatalf("evidence refs = %v, want event %s", call.evidenceRefs, eventID)
		}
	}
}

// TestMemoryUsecase_Extract_DeclarativeRememberPhrasesAreNotRememberIntent
// pins the imperative gate so declarative prose like "I remember that we
// already fixed this" is no longer promoted as a remember-intent candidate by
// the inline trigger parser. Each case must end with no Propose call.
func TestMemoryUsecase_Extract_DeclarativeRememberPhrasesAreNotRememberIntent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "first-person declarative remember that",
			body: "I remember that we already fixed this",
		},
		{
			name: "first-person past tense remember this",
			body: "I remember this from the migration last quarter",
		},
		{
			name: "third-person declarative remember",
			body: "she remember that the build was broken",
		},
		{
			name: "subordinate clause remember that",
			body: "the docs say I remember that we always run tests",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := apptypes.SessionSummaryOf(domtypes.SessionID("session-decl-remember"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
			promptEvent := mustExtractionEvent(t, "event-decl-remember", domtypes.EventKindPrompt, tc.body)
			memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
			sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
			eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
			sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

			_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-decl-remember")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			for _, call := range memoryUsecase.proposeCalls {
				if call.source == domtypes.MemorySourceRememberIntent {
					t.Fatalf("declarative phrasing %q must not produce remember-intent candidate (got fact %q)", tc.body, call.fact)
				}
			}
		})
	}
}

func TestMemoryUsecase_Extract_JapaneseDeclarativeRememberPhrasesAreNotRememberIntent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "thanks for remembering",
			body: "覚えておいてくれてありがとう",
		},
		{
			name: "polite continuation without fact",
			body: "覚えておいてくれると助かります",
		},
		{
			name: "thanks for remembering politely",
			body: "覚えておいてくださってありがとうございます",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := apptypes.SessionSummaryOf(domtypes.SessionID("session-ja-decl-remember"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
			promptEvent := mustExtractionEvent(t, "event-ja-decl-remember", domtypes.EventKindPrompt, tc.body)
			memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
			sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
			eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
			sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

			_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-ja-decl-remember")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			for _, call := range memoryUsecase.proposeCalls {
				if call.source == domtypes.MemorySourceRememberIntent {
					t.Fatalf("Japanese declarative phrasing %q must not produce remember-intent candidate (got fact %q)", tc.body, call.fact)
				}
			}
		})
	}
}

func TestMemoryUsecase_Extract_DeclarativeRememberStillUsesHeuristics(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-decl-remember-heuristic"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-decl-remember-heuristic", domtypes.EventKindPrompt, "I remember that we should always run go test before merging.")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-decl-remember-heuristic")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1 heuristic candidate", len(memoryUsecase.proposeCalls))
	}
	call := memoryUsecase.proposeCalls[0]
	if call.source == domtypes.MemorySourceRememberIntent {
		t.Fatalf("source = %q, want heuristic extraction rather than remember-intent", call.source)
	}
	if call.fact != "I remember that we should always run go test before merging." {
		t.Fatalf("fact = %q, want original heuristic fact", call.fact)
	}
}

func TestMemoryUsecase_Extract_InlineRememberContinuesAfterDeclarativeMatch(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-decl-then-imperative"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-decl-then-imperative", domtypes.EventKindPrompt, "I remember that this was flaky, remember this: run tests first")
	details := mustMemoryDetailsFromSummary(t, "memory-decl-then-imperative", domtypes.MemoryTypeLesson, "run tests first")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-decl-then-imperative")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1 imperative remember candidate", len(memoryUsecase.proposeCalls))
	}
	call := memoryUsecase.proposeCalls[0]
	if call.source != domtypes.MemorySourceRememberIntent {
		t.Fatalf("source = %q, want remember-intent", call.source)
	}
	if call.fact != "run tests first" {
		t.Fatalf("fact = %q, want later imperative fact", call.fact)
	}
}

func TestMemoryUsecase_Extract_JapaneseInlineRememberContinuesAfterDeclarativeMatch(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-ja-decl-then-imperative"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-ja-decl-then-imperative", domtypes.EventKindPrompt, "覚えておいてくれてありがとう。覚えておいて: go test before merge")
	details := mustMemoryDetailsFromSummary(t, "memory-ja-decl-then-imperative", domtypes.MemoryTypeLesson, "go test before merge")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-ja-decl-then-imperative")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1 imperative remember candidate", len(memoryUsecase.proposeCalls))
	}
	call := memoryUsecase.proposeCalls[0]
	if call.source != domtypes.MemorySourceRememberIntent {
		t.Fatalf("source = %q, want remember-intent", call.source)
	}
	if call.fact != "go test before merge" {
		t.Fatalf("fact = %q, want later Japanese imperative fact", call.fact)
	}
}

// TestMemoryUsecase_Extract_TriggerOnlyNegationsRejected ensures that prompts
// like "Don't remember this." or "do not remember this" never produce durable
// memory candidates. The inline trigger parser used to treat the leading text
// as the fact when no factual continuation followed the trigger.
func TestMemoryUsecase_Extract_TriggerOnlyNegationsRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "english don't trigger negation",
			body: "Don't remember this.",
		},
		{
			name: "english do not trigger negation",
			body: "do not remember this",
		},
		{
			name: "english never remember trigger",
			body: "never remember this",
		},
		{
			name: "english trailing don't keep this in mind",
			body: "Don't keep this in mind.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := apptypes.SessionSummaryOf(domtypes.SessionID("session-neg-remember"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
			promptEvent := mustExtractionEvent(t, "event-neg-remember", domtypes.EventKindPrompt, tc.body)
			memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
			sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
			eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
			sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

			_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-neg-remember")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			for _, call := range memoryUsecase.proposeCalls {
				if call.source == domtypes.MemorySourceRememberIntent {
					t.Fatalf("negation prompt %q must not produce remember-intent candidate (got fact %q)", tc.body, call.fact)
				}
			}
		})
	}
}

// TestMemoryUsecase_Extract_ShortRememberIntentDoesNotBindStaleContext pins
// the bounded lookback so a short remember-only prompt does not pull a
// factual context from many turns earlier when intervening turns are
// non-factual chatter. The earliest context event must NOT be promoted.
func TestMemoryUsecase_Extract_ShortRememberIntentDoesNotBindStaleContext(t *testing.T) {
	t.Parallel()

	now := time.Now()
	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-remember-stale"), domtypes.Workspace("github.com/duck8823/traceary"), now.Add(-time.Hour), domtypes.None[time.Time](), "ended", 6, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	staleFactualEvent := mustExtractionEventAt(t, "event-stale-context", domtypes.EventKindPrompt, "Always run go test before merging.", now)
	noisePrompts := []*model.Event{
		mustExtractionEventAt(t, "event-noise-prompt-1", domtypes.EventKindPrompt, "Hmm.", now.Add(time.Second)),
		mustExtractionEventAt(t, "event-noise-prompt-2", domtypes.EventKindPrompt, "Got it.", now.Add(2*time.Second)),
		mustExtractionEventAt(t, "event-noise-prompt-3", domtypes.EventKindPrompt, "Right.", now.Add(3*time.Second)),
	}
	rememberEvent := mustExtractionEventAt(t, "event-remember-far", domtypes.EventKindPrompt, "覚えておいてね", now.Add(4*time.Second))
	allPrompts := append([]*model.Event{rememberEvent}, noisePrompts...)
	allPrompts = append(allPrompts, staleFactualEvent)
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt: allPrompts,
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-remember-stale")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(10).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	for _, call := range memoryUsecase.proposeCalls {
		if call.source == domtypes.MemorySourceRememberIntent {
			t.Fatalf("stale factual context far behind a short remember prompt must not be linked: got fact %q", call.fact)
		}
	}
}

// TestMemoryUsecase_Extract_RememberIntentContextSpecsPreserveRecency pins
// that when multiple short remember-only prompts appear, the most recent
// one's context candidate is preferred when CandidateLimit caps the result.
func TestMemoryUsecase_Extract_RememberIntentContextSpecsPreserveRecency(t *testing.T) {
	t.Parallel()

	now := time.Now()
	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-remember-recency"), domtypes.Workspace("github.com/duck8823/traceary"), now.Add(-time.Hour), domtypes.None[time.Time](), "ended", 4, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	olderContext := mustExtractionEventAt(t, "event-older-context", domtypes.EventKindPrompt, "Always run go test before merging.", now)
	olderRemember := mustExtractionEventAt(t, "event-older-remember", domtypes.EventKindPrompt, "覚えておいてね", now.Add(time.Second))
	newerContext := mustExtractionEventAt(t, "event-newer-context", domtypes.EventKindPrompt, "Prefer Japanese answers in this repository.", now.Add(2*time.Second))
	newerRemember := mustExtractionEventAt(t, "event-newer-remember", domtypes.EventKindPrompt, "覚えておいてね", now.Add(3*time.Second))
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt: {newerRemember, newerContext, olderRemember, olderContext},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-remember-recency")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(10).CandidateLimit(1).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1 (CandidateLimit=1)", len(memoryUsecase.proposeCalls))
	}
	got := memoryUsecase.proposeCalls[0]
	if got.fact != "Prefer Japanese answers in this repository." {
		t.Fatalf("fact = %q, want newer context fact (recency preserved)", got.fact)
	}
}

func TestMemoryUsecase_ExplainExtraction_ReportsShortRememberIntentContextCandidate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-debug-remember-context"), domtypes.Workspace("github.com/duck8823/traceary"), now.Add(-time.Hour), domtypes.None[time.Time](), "ended", 2, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	contextEvent := mustExtractionEventAt(t, "event-debug-context", domtypes.EventKindPrompt, "Please answer in Japanese for this repository.", now)
	rememberEvent := mustExtractionEventAt(t, "event-debug-remember-short", domtypes.EventKindPrompt, "覚えておいてね", now.Add(time.Second))
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{
		listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
			domtypes.EventKindPrompt: {rememberEvent, contextEvent},
		},
	}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	report, err := sut.ExplainExtraction(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-debug-remember-context")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(2).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("ExplainExtraction() error = %v", err)
	}

	var proposed *apptypes.MemoryExtractionSegmentDecision
	foundContextOnlyPrompt := false
	for index := range report.Segments {
		segment := &report.Segments[index]
		if segment.Decision == "ignored" && segment.Reason == "remember_intent_context_only" && segment.Text == "覚えておいてね" {
			foundContextOnlyPrompt = true
		}
		if segment.Decision == "proposed" && segment.Text == "Please answer in Japanese for this repository." {
			proposed = segment
		}
	}
	if !foundContextOnlyPrompt {
		t.Fatalf("segments = %+v, want short remember prompt marked remember_intent_context_only", report.Segments)
	}
	if proposed == nil {
		t.Fatalf("segments = %+v, want proposed adjacent-context candidate", report.Segments)
	}
	if proposed.MemoryType != domtypes.MemoryTypePreference {
		t.Fatalf("proposed memory type = %q, want preference", proposed.MemoryType)
	}
	if !slices.Contains(proposed.Features, "explicit_remember") {
		t.Fatalf("proposed features = %v, want explicit_remember", proposed.Features)
	}
	gotEvidence := make(map[string]bool)
	for _, ref := range proposed.EvidenceRefs {
		if ref.Kind() == domtypes.EvidenceRefKindEvent {
			gotEvidence[ref.Value()] = true
		}
	}
	for _, eventID := range []string{"event-debug-remember-short", "event-debug-context"} {
		if !gotEvidence[eventID] {
			t.Fatalf("proposed evidence refs = %v, want event %s", proposed.EvidenceRefs, eventID)
		}
	}
}

// TestMemoryUsecase_ExplainExtraction_DeclarativeRememberPhrasingNotPromoted
// pins that the debug report agrees with Extract on the imperative gate so an
// operator diagnosing why a candidate was created sees the same decision.
// Declarative "I remember that ..." prose must NOT appear as a proposed
// remember-intent candidate in the debug report.
func TestMemoryUsecase_ExplainExtraction_DeclarativeRememberPhrasingNotPromoted(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-debug-decl-remember"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"claude"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-debug-decl-remember", domtypes.EventKindPrompt, "I remember that we already fixed this")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	report, err := sut.ExplainExtraction(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-debug-decl-remember")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("ExplainExtraction() error = %v", err)
	}
	for _, segment := range report.Segments {
		if segment.Decision == "proposed" && slices.Contains(segment.Features, "explicit_remember") {
			t.Fatalf("declarative remember phrasing surfaced as proposed remember-intent: %+v", segment)
		}
	}
}

func TestMemoryUsecase_ExplainExtraction_ReportsIgnoredAndProposedSegments(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-debug"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 2, 0, []string{"codex"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-debug", domtypes.EventKindPrompt, strings.Join([]string{
		"I ran the command and checked the output.",
		"Remember: Poll Codex review status before assuming it timed out.",
	}, "\n"))
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	report, err := sut.ExplainExtraction(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-debug")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("ExplainExtraction() error = %v", err)
	}
	if len(report.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(report.Segments))
	}
	if report.Segments[0].Decision != "ignored" || report.Segments[0].Reason != "no_memory_intent" {
		t.Fatalf("first segment decision = %s/%s, want ignored/no_memory_intent", report.Segments[0].Decision, report.Segments[0].Reason)
	}
	second := report.Segments[1]
	if second.Decision != "proposed" {
		t.Fatalf("second decision = %s, want proposed", second.Decision)
	}
	if second.MemoryType != domtypes.MemoryTypeLesson {
		t.Fatalf("second memory type = %q, want lesson", second.MemoryType)
	}
	if !slices.Contains(second.Features, "explicit_remember") {
		t.Fatalf("features = %v, want explicit_remember", second.Features)
	}
	if second.Score < 4 {
		t.Fatalf("score = %d, want visible threshold", second.Score)
	}
}

func TestMemoryUsecase_Extract_DoesNotTreatMetricMemoryLabelAsDurableIntent(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-memory-metric"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"codex"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-memory-metric", domtypes.EventKindPrompt, "Memory: 2 GB")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-memory-metric")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 0 || len(memoryUsecase.proposeCalls) != 0 {
		t.Fatalf("metric memory line produced candidates: got=%d proposeCalls=%d", len(got), len(memoryUsecase.proposeCalls))
	}
}

func TestMemoryUsecase_ExplainExtraction_MarksDuplicateInRun(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-debug-duplicates"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"codex"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-debug-duplicates", domtypes.EventKindPrompt, strings.Join([]string{
		"must fix",
		"Constraint: must fix",
	}, "\n"))
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	report, err := sut.ExplainExtraction(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-debug-duplicates")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("ExplainExtraction() error = %v", err)
	}
	if len(report.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(report.Segments))
	}
	if report.Segments[0].Decision != "skipped" || report.Segments[0].Reason != "duplicate_in_run" {
		t.Fatalf("first decision = %s/%s, want skipped/duplicate_in_run", report.Segments[0].Decision, report.Segments[0].Reason)
	}
	if report.Segments[1].Decision != "proposed" {
		t.Fatalf("second decision = %s, want proposed", report.Segments[1].Decision)
	}
}

func TestMemoryUsecase_Extract_DoesNotTreatJapaneseMemoryMetricAsDurableIntent(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-ja-memory-metric"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"codex"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-ja-memory-metric", domtypes.EventKindPrompt, "メモリ: 2 GB")
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	got, err := sut.Extract(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-ja-memory-metric")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(10).Build())
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 0 || len(memoryUsecase.proposeCalls) != 0 {
		t.Fatalf("Japanese memory metric produced candidates: got=%d proposeCalls=%d", len(got), len(memoryUsecase.proposeCalls))
	}
}

func TestMemoryUsecase_ExplainExtraction_RespectsCandidateLimit(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-debug-limit"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"codex"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-debug-limit", domtypes.EventKindPrompt, strings.Join([]string{
		"Remember: Poll Codex review status before assuming it timed out.",
		"Remember: Check release workflow before announcing completion.",
	}, "\n"))
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	report, err := sut.ExplainExtraction(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-debug-limit")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(1).Build())
	if err != nil {
		t.Fatalf("ExplainExtraction() error = %v", err)
	}
	if len(report.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(report.Segments))
	}
	if report.Segments[0].Decision != "proposed" {
		t.Fatalf("first decision = %s, want proposed", report.Segments[0].Decision)
	}
	if report.Segments[1].Decision != "skipped" || report.Segments[1].Reason != "candidate_limit" {
		t.Fatalf("second decision = %s/%s, want skipped/candidate_limit", report.Segments[1].Decision, report.Segments[1].Reason)
	}
}

func TestMemoryUsecase_ExplainExtraction_AppliesCandidateLimitInExtractionKeyOrder(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(domtypes.SessionID("session-debug-limit-key-order"), domtypes.Workspace("github.com/duck8823/traceary"), time.Now().Add(-time.Hour), domtypes.None[time.Time](), "ended", 1, 0, []string{"codex"}, "", "", domtypes.SessionID(""))
	promptEvent := mustExtractionEvent(t, "event-debug-limit-key-order", domtypes.EventKindPrompt, strings.Join([]string{
		"must fix",
		"Remember: Check release workflow before announcing completion.",
		"Constraint: must fix",
	}, "\n"))
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	report, err := sut.ExplainExtraction(context.Background(), apptypes.NewMemoryExtractionCriteriaBuilder().SessionID(domtypes.SessionID("session-debug-limit-key-order")).Workspace(domtypes.Workspace("github.com/duck8823/traceary")).EventLimit(1).CandidateLimit(1).Build())
	if err != nil {
		t.Fatalf("ExplainExtraction() error = %v", err)
	}
	if len(report.Segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(report.Segments))
	}
	if report.Segments[0].Decision != "skipped" || report.Segments[0].Reason != "duplicate_in_run" {
		t.Fatalf("first decision = %s/%s, want skipped/duplicate_in_run", report.Segments[0].Decision, report.Segments[0].Reason)
	}
	if report.Segments[1].Decision != "skipped" || report.Segments[1].Reason != "candidate_limit" {
		t.Fatalf("second decision = %s/%s, want skipped/candidate_limit", report.Segments[1].Decision, report.Segments[1].Reason)
	}
	if report.Segments[2].Decision != "proposed" {
		t.Fatalf("third decision = %s, want proposed", report.Segments[2].Decision)
	}
}

// TestMemoryUsecase_Extract_HidesNoisyCandidates verifies that diff fragments,
// standalone commands, generated-code markers, review-only conclusions, work
// declarations, and PR/Round chatter are routed to the extracted-hidden
// source so the default `memory inbox list` view stays clean (#857).
func TestMemoryUsecase_Extract_HidesNoisyCandidates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     string
		wantFact string
	}{
		{
			name:     "diff fragment under structured label",
			body:     "Decision: +def _required_env(name):",
			wantFact: "+def _required_env(name):",
		},
		{
			name:     "standalone command under structured label",
			body:     "Lesson: git pull --ff-only origin main",
			wantFact: "git pull --ff-only origin main",
		},
		{
			name:     "generated code marker under structured label",
			body:     "Lesson: +// Code generated by MockGen. DO NOT EDIT.",
			wantFact: "+// Code generated by MockGen. DO NOT EDIT.",
		},
		{
			name:     "review conclusion under structured label",
			body:     "Lesson: MUST findings: none for this round",
			wantFact: "MUST findings: none for this round",
		},
		{
			name:     "work declaration under structured label",
			body:     "Decision: I will read presentation/cli/memory_inbox.go",
			wantFact: "I will read presentation/cli/memory_inbox.go",
		},
		{
			name:     "transient pr round chatter under structured label",
			body:     "Lesson: Round 3 follow-up notes are in the PR",
			wantFact: "Round 3 follow-up notes are in the PR",
		},
		{
			name:     "japanese review conclusion under structured label",
			body:     "教訓: 確認済み・問題なしです",
			wantFact: "確認済み・問題なしです",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := apptypes.SessionSummaryOf(
				domtypes.SessionID("session-noise"),
				domtypes.Workspace("github.com/duck8823/traceary"),
				time.Now().Add(-time.Hour),
				domtypes.None[time.Time](),
				"ended",
				1,
				0,
				[]string{"codex"},
				"",
				"",
				domtypes.SessionID(""),
			)
			promptEvent := mustExtractionEvent(t, "event-noise", domtypes.EventKindPrompt, tc.body)
			details := mustMemoryDetailsFromSummary(t, "memory-noise", domtypes.MemoryTypeLesson, tc.wantFact)
			memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
			sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
			eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
			sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

			_, err := sut.Extract(
				context.Background(),
				apptypes.NewMemoryExtractionCriteriaBuilder().
					SessionID(domtypes.SessionID("session-noise")).
					Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
					EventLimit(1).
					CandidateLimit(10).
					Build(),
			)
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if len(memoryUsecase.proposeCalls) != 1 {
				t.Fatalf("proposeCalls = %d, want 1 hidden noise candidate", len(memoryUsecase.proposeCalls))
			}
			if got := memoryUsecase.proposeCalls[0].fact; got != tc.wantFact {
				t.Fatalf("fact = %q, want %q", got, tc.wantFact)
			}
			if got := memoryUsecase.proposeCalls[0].source; got != domtypes.MemorySourceExtractedHidden {
				t.Fatalf("source = %q, want extracted-hidden", got)
			}
		})
	}
}

// TestMemoryUsecase_Extract_KeepsExplicitRememberVisibleEvenWhenNoisy verifies
// that explicit user remember-intent overrides the noise classifier so an
// operator-driven `remember this:` prompt always remains visible. (#857)
func TestMemoryUsecase_Extract_KeepsExplicitRememberVisibleEvenWhenNoisy(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-noise-remember"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		1,
		0,
		[]string{"codex"},
		"",
		"",
		domtypes.SessionID(""),
	)
	promptEvent := mustExtractionEvent(t,
		"event-noise-remember",
		domtypes.EventKindPrompt,
		"Remember this: git pull --ff-only origin main is the safe default for CI",
	)
	details := mustMemoryDetailsFromSummary(t,
		"memory-noise-remember",
		domtypes.MemoryTypeLesson,
		"git pull --ff-only origin main is the safe default for CI",
	)
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{proposeResult: []apptypes.MemoryDetails{details}}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	_, err := sut.Extract(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-noise-remember")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(1).
			CandidateLimit(10).
			Build(),
	)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memoryUsecase.proposeCalls) != 1 {
		t.Fatalf("proposeCalls = %d, want 1 visible candidate", len(memoryUsecase.proposeCalls))
	}
	if got := memoryUsecase.proposeCalls[0].source; got != domtypes.MemorySourceRememberIntent {
		t.Fatalf("source = %q, want remember-intent (explicit remember overrides noise)", got)
	}
}

// TestMemoryUsecase_ExplainExtraction_ReportsLowQualityReasons verifies that
// `memory extract --debug-signals` surfaces the deterministic noise reason
// for each segment so operators can audit why a candidate was hidden (#857).
func TestMemoryUsecase_ExplainExtraction_ReportsLowQualityReasons(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-debug-noise"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.None[time.Time](),
		"ended",
		1,
		0,
		[]string{"codex"},
		"",
		"",
		domtypes.SessionID(""),
	)
	promptEvent := mustExtractionEvent(t,
		"event-debug-noise",
		domtypes.EventKindPrompt,
		strings.Join([]string{
			"Decision: git pull --ff-only origin main",
			"Lesson: Wait for explicit review completion before merge",
		}, "\n"),
	)
	memoryUsecase := &memoryExtractionMemoryUsecaseStub{}
	sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
	eventQuery := &eventQueryServiceStub{listRecentResultByKind: map[domtypes.EventKind][]*model.Event{domtypes.EventKindPrompt: {promptEvent}}}
	sut := usecase.NewMemoryUsecase(memoryUsecase, memoryUsecase, nil, usecase.MemoryUsecaseDependencies{SessionQuery: sessionQuery, EventQuery: eventQuery})

	report, err := sut.ExplainExtraction(
		context.Background(),
		apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(domtypes.SessionID("session-debug-noise")).
			Workspace(domtypes.Workspace("github.com/duck8823/traceary")).
			EventLimit(1).
			CandidateLimit(10).
			Build(),
	)
	if err != nil {
		t.Fatalf("ExplainExtraction() error = %v", err)
	}
	if len(report.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(report.Segments))
	}
	noisy := report.Segments[0]
	if noisy.Decision != "hidden" {
		t.Fatalf("noisy decision = %q, want hidden", noisy.Decision)
	}
	if noisy.Reason != "low_quality:standalone_command" {
		t.Fatalf("noisy reason = %q, want low_quality:standalone_command", noisy.Reason)
	}
	if !slices.Contains(noisy.LowQualityReasons, "standalone_command") {
		t.Fatalf("noisy low_quality_reasons = %v, want to include standalone_command", noisy.LowQualityReasons)
	}
	clean := report.Segments[1]
	if clean.Decision != "proposed" {
		t.Fatalf("clean decision = %q, want proposed", clean.Decision)
	}
	if len(clean.LowQualityReasons) != 0 {
		t.Fatalf("clean low_quality_reasons = %v, want empty", clean.LowQualityReasons)
	}
}
