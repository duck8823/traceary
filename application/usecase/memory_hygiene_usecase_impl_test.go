package usecase_test

import (
	"context"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func acceptedSummaryAt(t *testing.T, id string, scope domtypes.MemoryScope, fact string, updatedAt time.Time) apptypes.MemorySummary {
	t.Helper()
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		scope,
		fact,
		domtypes.MemoryStatusAccepted,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		updatedAt,
		domtypes.None[time.Time](),
		updatedAt,
		updatedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}

func TestMemoryHygieneScan_DetectsRedactionExpiryAndDuplicates(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	now := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			// Redaction hit — content matches a user-supplied extra pattern
			// the operator added to their config after the memory was
			// accepted, so the stored fact leaks content the current
			// redaction pipeline would mask.
			acceptedSummaryAt(t, "mem-redact", scope, "keep internal-token-42", now.Add(-1*time.Hour)),
			// Expiry candidate — last updated 200 days ago.
			acceptedSummaryAt(t, "mem-stale", scope, "stale preference", now.Add(-200*24*time.Hour)),
			// Duplicate pair — same scope + same fact.
			acceptedSummaryAt(t, "mem-dup-1", scope, "prefer bulleted commits", now),
			acceptedSummaryAt(t, "mem-dup-2", scope, "prefer bulleted commits", now),
		},
	}
	sut := usecase.NewMemoryHygieneUsecase(&stubImportMemoryUsecase{}, query, []string{`internal-token-\d+`})

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{
		StalenessThreshold: 90 * 24 * time.Hour,
		Now:                now,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.RedactionHitCount == 0 {
		t.Fatalf("expected a redaction hit suggestion")
	}
	if result.ExpiryCandidateCount == 0 {
		t.Fatalf("expected an expiry candidate suggestion")
	}
	if result.DuplicateCount != 2 {
		t.Fatalf("expected a pair of duplicate suggestions, got %d", result.DuplicateCount)
	}
}

func TestMemoryHygieneScan_SimilarFactsEmitSupersedeCandidate(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	older := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			// Word overlap: {prefer, bulleted, commit, messages} vs
			// {prefer, bulleted, commit, messages, style} → Jaccard 4/5
			acceptedSummaryAt(t, "mem-older", scope, "prefer bulleted commit messages", older),
			acceptedSummaryAt(t, "mem-newer", scope, "prefer bulleted commit messages style", newer),
			// A third unrelated fact must not pair up with either of the
			// above — they share few enough words to stay below threshold.
			acceptedSummaryAt(t, "mem-unrelated", scope, "use semicolons in SQL migrations", newer),
		},
	}
	sut := usecase.NewMemoryHygieneUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: newer.Add(24 * time.Hour)})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.SupersedeCandidateCount != 1 {
		t.Fatalf("SupersedeCandidateCount = %d, want 1", result.SupersedeCandidateCount)
	}
	var supersede *apptypes.MemoryHygieneSuggestion
	for i, suggestion := range result.Suggestions {
		if suggestion.Kind == apptypes.MemoryHygieneSuggestionSupersedeCandidate {
			supersede = &result.Suggestions[i]
			break
		}
	}
	if supersede == nil {
		t.Fatalf("expected a supersede_candidate suggestion, got %+v", result.Suggestions)
	}
	if supersede.MemoryID.String() != "mem-older" {
		t.Fatalf("older memory should be the supersede target, got %s", supersede.MemoryID)
	}
	if supersede.ReplacementMemoryID.String() != "mem-newer" {
		t.Fatalf("newer memory should be the replacement, got %s", supersede.ReplacementMemoryID)
	}
	if supersede.Similarity < 0.5 || supersede.Similarity > 1.0 {
		t.Fatalf("similarity %.2f outside plausible range", supersede.Similarity)
	}
}

func TestMemoryHygieneScan_EmptyStoreReturnsEmptyResult(t *testing.T) {
	t.Parallel()

	query := &stubMemoryQueryService{}
	sut := usecase.NewMemoryHygieneUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Suggestions) != 0 {
		t.Fatalf("expected empty suggestions, got %d", len(result.Suggestions))
	}
	if result.RedactionHitCount+result.ExpiryCandidateCount+result.DuplicateCount != 0 {
		t.Fatalf("expected zero counts across all suggestion kinds, got r=%d e=%d d=%d", result.RedactionHitCount, result.ExpiryCandidateCount, result.DuplicateCount)
	}
}
