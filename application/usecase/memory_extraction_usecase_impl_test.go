package usecase_test

import (
	"context"
	"fmt"
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

func (s *memoryExtractionMemoryUsecaseStub) Supersede(context.Context, domtypes.MemoryID, domtypes.MemoryType, domtypes.MemoryScope, string, domtypes.Optional[domtypes.Confidence], domtypes.MemorySource, []domtypes.EvidenceRef, []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryExtractionMemoryUsecaseStub) Expire(context.Context, domtypes.MemoryID, domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error) {
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

func TestMemoryExtractionUsecase_Extract(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.Empty[time.Time](),
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

	sut := usecase.NewMemoryExtractionUsecase(
		&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
		&eventQueryServiceStub{
			listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
				domtypes.EventKindPrompt:         {promptEvent},
				domtypes.EventKindNote:           {noteEvent},
				domtypes.EventKindCompactSummary: {compactEvent},
			},
		},
		memoryUsecase,
		nil,
	)

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

func TestMemoryExtractionUsecase_Extract_DeduplicatesExistingAndGracefullyHandlesMissingPrompts(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.Empty[time.Time](),
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
		domtypes.Empty[domtypes.MemoryID](),
		domtypes.Empty[time.Time](),
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}

	memoryUsecase := &memoryExtractionMemoryUsecaseStub{
		listResult: []apptypes.MemorySummary{existingSummary},
	}
	sut := usecase.NewMemoryExtractionUsecase(
		&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
		&eventQueryServiceStub{},
		memoryUsecase,
		nil,
	)

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

func TestMemoryExtractionUsecase_Extract_PaginatesExistingMemoryDedupe(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.Empty[time.Time](),
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
			domtypes.Empty[domtypes.MemoryID](),
			domtypes.Empty[time.Time](),
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
		domtypes.Empty[domtypes.MemoryID](),
		domtypes.Empty[time.Time](),
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	listResult = append(listResult, duplicateSummary)

	memoryUsecase := &memoryExtractionMemoryUsecaseStub{listResult: listResult}
	sut := usecase.NewMemoryExtractionUsecase(
		&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
		&eventQueryServiceStub{},
		memoryUsecase,
		nil,
	)

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

func TestMemoryExtractionUsecase_Extract_DeduplicatesSanitizedFacts(t *testing.T) {
	t.Parallel()

	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		time.Now().Add(-time.Hour),
		domtypes.Empty[time.Time](),
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
	sut := usecase.NewMemoryExtractionUsecase(
		&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
		&eventQueryServiceStub{
			listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
				domtypes.EventKindPrompt: {promptEvent},
			},
		},
		memoryUsecase,
		nil,
	)

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
		domtypes.Empty[domtypes.MemoryID](),
		domtypes.Empty[time.Time](),
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	return apptypes.MemoryDetailsOf(summary, []domtypes.EvidenceRef{mustEvidenceRef(t, domtypes.EvidenceRefKindSession, "session-1")}, nil)
}
