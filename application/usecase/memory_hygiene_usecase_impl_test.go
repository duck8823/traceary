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

// validitySummary is a helper for the validity_overlap_supersede tests
// that sets an explicit valid_from / valid_to window on the memory
// alongside updated_at, so the detector sees a realistic temporal
// footprint.
func validitySummary(
	t *testing.T,
	id string,
	scope domtypes.MemoryScope,
	memoryType domtypes.MemoryType,
	fact string,
	updatedAt time.Time,
	validFrom time.Time,
	validTo domtypes.Optional[time.Time],
) apptypes.MemorySummary {
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
		validFrom,
		validTo,
		updatedAt,
		updatedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}

func TestMemoryHygieneScan_ValidityOverlapEmitsSupersede(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	t1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// Overlap case: both memories in same (scope, type), windows overlap,
	// facts differ and word Jaccard is high.
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			validitySummary(t, "mem-older", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon",
				t1, t1, domtypes.Some(t3)),
			validitySummary(t, "mem-newer", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon at 15:00",
				t2, t2, domtypes.None[time.Time]()),
		},
	}
	sut := usecase.NewMemoryHygieneUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: t3})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.ValidityOverlapSupersedeCount == 0 {
		t.Fatalf("expected at least one validity_overlap_supersede, suggestions=%+v", result.Suggestions)
	}
	var overlap *apptypes.MemoryHygieneSuggestion
	for i, suggestion := range result.Suggestions {
		if suggestion.Kind == apptypes.MemoryHygieneSuggestionValidityOverlapSupersede {
			overlap = &result.Suggestions[i]
			break
		}
	}
	if overlap == nil {
		t.Fatalf("no validity_overlap_supersede entry in suggestions=%+v", result.Suggestions)
	}
	if overlap.MemoryID.String() != "mem-older" {
		t.Fatalf("older memory should be the supersede target, got %s", overlap.MemoryID)
	}
	if overlap.ReplacementMemoryID.String() != "mem-newer" {
		t.Fatalf("newer memory should be the replacement, got %s", overlap.ReplacementMemoryID)
	}
}

func TestMemoryHygieneScan_DisjointValidityWindowsAreNotSuperseded(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// Disjoint windows: [t1, t2] then [t3, t4] — first memory retired
	// before the second took effect. The detector must treat them as
	// separate historical facts, not supersede candidates.
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			validitySummary(t, "mem-q1", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon",
				t1, t1, domtypes.Some(t2)),
			validitySummary(t, "mem-q2", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon at 15:00",
				t3, t3, domtypes.Some(t4)),
		},
	}
	sut := usecase.NewMemoryHygieneUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: t4})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.ValidityOverlapSupersedeCount != 0 {
		t.Fatalf("disjoint validity windows must not trigger validity_overlap_supersede, got count=%d", result.ValidityOverlapSupersedeCount)
	}
}

func TestMemoryHygieneScan_BothOpenEndedWindowsAreNotValidityOverlap(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	t1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// Same scope, same type, similar facts, but BOTH memories have
	// open-ended validity windows (valid_to=None). The validity-overlap
	// detector must fall through — supersede_candidate handles this
	// without temporal evidence — even though scopeTypeKey would
	// otherwise group them. Regression guard for validityWindowsOverlap
	// returning true when neither side has an explicit upper bound.
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			validitySummary(t, "mem-a", scope, domtypes.MemoryTypePreference,
				"prefer bulleted commit messages",
				t1, t1, domtypes.None[time.Time]()),
			validitySummary(t, "mem-b", scope, domtypes.MemoryTypePreference,
				"prefer bulleted commit messages style",
				t2, t2, domtypes.None[time.Time]()),
		},
	}
	sut := usecase.NewMemoryHygieneUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: t2.Add(24 * time.Hour)})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.ValidityOverlapSupersedeCount != 0 {
		t.Fatalf("both open-ended validity windows must not trigger validity_overlap_supersede, got count=%d", result.ValidityOverlapSupersedeCount)
	}
	if result.SupersedeCandidateCount != 1 {
		t.Fatalf("generic supersede_candidate should still catch the pair, got count=%d", result.SupersedeCandidateCount)
	}
}

func TestMemoryHygieneScan_ValidityOverlapDifferentTypesDoNotPair(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	t1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// Same scope, overlapping windows, similar facts, but different
	// types — the pair must not be reported because type divergence
	// means they are conceptually different knowledge objects.
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			validitySummary(t, "mem-preference", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon",
				t1, t1, domtypes.None[time.Time]()),
			validitySummary(t, "mem-lesson", scope, domtypes.MemoryTypeLesson,
				"deploy to staging every afternoon at 15:00",
				t2, t2, domtypes.None[time.Time]()),
		},
	}
	sut := usecase.NewMemoryHygieneUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: t2.Add(24 * time.Hour)})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.ValidityOverlapSupersedeCount != 0 {
		t.Fatalf("different memory types must not pair, got count=%d", result.ValidityOverlapSupersedeCount)
	}
}

func TestMemoryHygieneScan_ValidityOverlapOverridesSupersedeCandidate(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	t1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Pair qualifies for both detectors (same scope + similar facts for
	// SupersedeCandidate, same type + overlapping windows for
	// ValidityOverlap — the older memory has an explicit valid_to so
	// the validity detector triggers). The more specific detector
	// wins: the pair is reported only under validity_overlap_supersede
	// so the reviewer does not see it twice.
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			validitySummary(t, "mem-older", scope, domtypes.MemoryTypePreference,
				"prefer bulleted commit messages",
				t1, t1, domtypes.Some(t3)),
			validitySummary(t, "mem-newer", scope, domtypes.MemoryTypePreference,
				"prefer bulleted commit messages style",
				t2, t2, domtypes.None[time.Time]()),
		},
	}
	sut := usecase.NewMemoryHygieneUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: t2.Add(24 * time.Hour)})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.SupersedeCandidateCount != 0 {
		t.Fatalf("SupersedeCandidateCount = %d, want 0 (validity_overlap should absorb the pair)", result.SupersedeCandidateCount)
	}
	if result.ValidityOverlapSupersedeCount != 1 {
		t.Fatalf("ValidityOverlapSupersedeCount = %d, want 1", result.ValidityOverlapSupersedeCount)
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
