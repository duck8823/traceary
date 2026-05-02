package usecase_test

import (
	"context"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
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

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, []string{`internal-token-\d+`})

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

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

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

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

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

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: t4})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.ValidityOverlapSupersedeCount != 0 {
		t.Fatalf("disjoint validity windows must not trigger validity_overlap_supersede, got count=%d", result.ValidityOverlapSupersedeCount)
	}
	if result.SupersedeCandidateCount != 0 {
		t.Fatalf("temporally-bounded disjoint pairs must not fall through to supersede_candidate, got count=%d", result.SupersedeCandidateCount)
	}
}

func TestMemoryHygieneScan_TouchingValidityWindowsAreDisjoint(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// Adjacent windows [t1, t2) and [t2, t3) must be treated as
	// disjoint under half-open semantics — runtime validity evaluates
	// valid_to as exclusive, so the two memories are never
	// simultaneously valid.
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			validitySummary(t, "mem-first", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon",
				t1, t1, domtypes.Some(t2)),
			validitySummary(t, "mem-second", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon at 15:00",
				t2, t2, domtypes.Some(t3)),
		},
	}
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: t3})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.ValidityOverlapSupersedeCount != 0 {
		t.Fatalf("touching [t1,t2) / [t2,t3) windows must not overlap under half-open semantics, got count=%d", result.ValidityOverlapSupersedeCount)
	}
	if result.SupersedeCandidateCount != 0 {
		t.Fatalf("touching windows are separate historical facts and must not become supersede_candidate either, got count=%d", result.SupersedeCandidateCount)
	}
}

func TestMemoryHygieneScan_OneOpenOneExplicitOverlapEmitsValidity(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// One side carries an explicit valid_to that sits after the other
	// side's valid_from, so the windows overlap under half-open
	// semantics. Only one bound needs to be explicit for the detector
	// to activate.
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			validitySummary(t, "mem-explicit", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon",
				t1, t1, domtypes.Some(t3)),
			validitySummary(t, "mem-open", scope, domtypes.MemoryTypePreference,
				"deploy to staging every afternoon at 15:00",
				t2, t2, domtypes.None[time.Time]()),
		},
	}
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: t3.Add(24 * time.Hour)})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.ValidityOverlapSupersedeCount != 1 {
		t.Fatalf("one explicit + one open overlap must emit validity_overlap_supersede, got count=%d", result.ValidityOverlapSupersedeCount)
	}
	if result.SupersedeCandidateCount != 0 {
		t.Fatalf("pair captured by validity_overlap should not also appear as supersede_candidate, got count=%d", result.SupersedeCandidateCount)
	}
}

func TestMemoryHygieneScan_BothOpenEndedWindowsAreNotValidityOverlap(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

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

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

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

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
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
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

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
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Suggestions) != 0 {
		t.Fatalf("expected empty suggestions, got %d", len(result.Suggestions))
	}
	if result.RedactionHitCount+result.ExpiryCandidateCount+result.DuplicateCount+result.LowQualityCandidateCount != 0 {
		t.Fatalf("expected zero counts across all suggestion kinds, got r=%d e=%d d=%d l=%d", result.RedactionHitCount, result.ExpiryCandidateCount, result.DuplicateCount, result.LowQualityCandidateCount)
	}
}

// candidateSummary builds a status=candidate MemorySummary with the
// supplied source so the candidate hygiene tests can flip between
// extracted (visible by default) and extracted-hidden (only inspected
// when the caller opts in).
func candidateSummary(t *testing.T, id string, scope domtypes.MemoryScope, fact string, source domtypes.MemorySource, updatedAt time.Time) apptypes.MemorySummary {
	t.Helper()
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		scope,
		fact,
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		source,
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

func TestMemoryHygieneScan_LowQualityCandidatesSurfaceWithReasons(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// Each candidate represents one of the noisy patterns the issue
	// calls out: diff fragment, standalone command, and review-only
	// conclusion. The classifier already covers more reasons but the
	// regression bar is "at least diff fragments, standalone commands,
	// and review-conclusion noise" per #864.
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			candidateSummary(t, "mem-diff", scope, "+def _required_env(name):", domtypes.MemorySourceExtracted, now),
			candidateSummary(t, "mem-cmd", scope, "git pull --ff-only origin main", domtypes.MemorySourceExtracted, now),
			candidateSummary(t, "mem-review", scope, "MUST findings: none", domtypes.MemorySourceExtracted, now),
			// High-signal candidate must remain untouched even though
			// it is also status=candidate.
			candidateSummary(t, "mem-keep", scope, "Always run go test before merging", domtypes.MemorySourceExtracted, now),
			// Hidden source must NOT be inspected unless the caller
			// opts in (see TestMemoryHygieneScan_HiddenCandidatesRequireOptIn).
			candidateSummary(t, "mem-hidden", scope, "+old_helper()", domtypes.MemorySourceExtractedHidden, now),
		},
	}
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{Now: now})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.LowQualityCandidateCount != 3 {
		t.Fatalf("LowQualityCandidateCount = %d, want 3 (diff/cmd/review)", result.LowQualityCandidateCount)
	}
	flagged := map[string]apptypes.MemoryHygieneSuggestion{}
	for _, suggestion := range result.Suggestions {
		if suggestion.Kind == apptypes.MemoryHygieneSuggestionLowQualityCandidate {
			flagged[suggestion.MemoryID.String()] = suggestion
		}
	}
	for _, want := range []string{"mem-diff", "mem-cmd", "mem-review"} {
		suggestion, ok := flagged[want]
		if !ok {
			t.Fatalf("expected %s to be flagged as low_quality_candidate, got %v", want, flagged)
		}
		if suggestion.Status != domtypes.MemoryStatusCandidate {
			t.Fatalf("%s: status = %q, want candidate", want, suggestion.Status)
		}
		if suggestion.Source != domtypes.MemorySourceExtracted {
			t.Fatalf("%s: source = %q, want extracted", want, suggestion.Source)
		}
		if len(suggestion.QualityReasons) == 0 {
			t.Fatalf("%s: QualityReasons must not be empty", want)
		}
		if suggestion.Reason == "" {
			t.Fatalf("%s: Reason must not be empty", want)
		}
	}
	if _, leaked := flagged["mem-keep"]; leaked {
		t.Fatalf("durable preference must not be flagged as low-quality")
	}
	if _, leaked := flagged["mem-hidden"]; leaked {
		t.Fatalf("extracted-hidden candidate must require --include-hidden to surface, got %+v", flagged["mem-hidden"])
	}

	expectedReasons := map[string]string{
		"mem-diff":   "diff_fragment",
		"mem-cmd":    "standalone_command",
		"mem-review": "review_conclusion",
	}
	for id, marker := range expectedReasons {
		suggestion := flagged[id]
		matched := false
		for _, reason := range suggestion.QualityReasons {
			if reason == marker {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("%s: expected QualityReasons to include %q, got %v", id, marker, suggestion.QualityReasons)
		}
	}
}

func TestMemoryHygieneScan_HiddenCandidatesRequireOptIn(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{
			candidateSummary(t, "mem-hidden", scope, "+old_helper()", domtypes.MemorySourceExtractedHidden, now),
		},
	}
	sut := usecase.NewMemoryUsecase(&stubImportMemoryUsecase{}, query, nil)

	result, err := sut.Scan(context.Background(), apptypes.MemoryHygieneScanCriteria{
		Now:                     now,
		IncludeHiddenCandidates: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.LowQualityCandidateCount != 1 {
		t.Fatalf("LowQualityCandidateCount = %d, want 1 once hidden candidates are inspected", result.LowQualityCandidateCount)
	}
	var flagged *apptypes.MemoryHygieneSuggestion
	for i := range result.Suggestions {
		if result.Suggestions[i].Kind == apptypes.MemoryHygieneSuggestionLowQualityCandidate {
			flagged = &result.Suggestions[i]
			break
		}
	}
	if flagged == nil {
		t.Fatalf("expected the hidden candidate to be flagged with IncludeHiddenCandidates=true")
	}
	if flagged.MemoryID.String() != "mem-hidden" {
		t.Fatalf("MemoryID = %q, want mem-hidden", flagged.MemoryID)
	}
	if flagged.Source != domtypes.MemorySourceExtractedHidden {
		t.Fatalf("Source = %q, want extracted-hidden", flagged.Source)
	}
}

// candidateApplyMemoryRepository is a minimal model.MemoryRepository
// stub for the candidate apply tests. It serves the candidate memory
// ids the test seeds, records the ids that flowed through Save() so we
// can assert the lifecycle transition the apply path drove, and refuses
// to surface accepted ids — so any test that would otherwise mutate an
// accepted memory through the candidate path fails fast.
type candidateApplyMemoryRepository struct {
	candidates map[string]*model.Memory
	saved      []*model.Memory
	saveErr    error
	findErr    error
}

func newCandidateApplyMemoryRepository(t *testing.T, scope domtypes.MemoryScope, candidates map[string]string) *candidateApplyMemoryRepository {
	t.Helper()
	repo := &candidateApplyMemoryRepository{candidates: make(map[string]*model.Memory, len(candidates))}
	for id, fact := range candidates {
		evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindFile, "/tmp/MEMORY.md#L1-L1")
		if err != nil {
			t.Fatalf("EvidenceRefFrom: %v", err)
		}
		memory, err := model.NewMemoryCandidate(
			domtypes.MemoryID(id),
			domtypes.MemoryTypePreference,
			scope,
			fact,
			domtypes.MemorySourceExtracted,
			[]domtypes.EvidenceRef{evidence},
			nil,
			domtypes.None[domtypes.MemoryID](),
		)
		if err != nil {
			t.Fatalf("NewMemoryCandidate: %v", err)
		}
		repo.candidates[id] = memory
	}
	return repo
}

func (r *candidateApplyMemoryRepository) Save(_ context.Context, memory *model.Memory) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, memory)
	return nil
}

func (r *candidateApplyMemoryRepository) SaveDistillation(context.Context, *model.Memory, []*model.Memory) error {
	return nil
}

func (r *candidateApplyMemoryRepository) SaveSupersession(context.Context, *model.Memory, *model.Memory) error {
	return nil
}

func (r *candidateApplyMemoryRepository) FindByID(_ context.Context, memoryID domtypes.MemoryID) (domtypes.Optional[*model.Memory], error) {
	if r.findErr != nil {
		return domtypes.None[*model.Memory](), r.findErr
	}
	if memory, ok := r.candidates[memoryID.String()]; ok {
		return domtypes.Some(memory), nil
	}
	return domtypes.None[*model.Memory](), nil
}

func (r *candidateApplyMemoryRepository) rejectedIDs() []string {
	ids := make([]string, 0, len(r.saved))
	for _, memory := range r.saved {
		if memory.Status() == domtypes.MemoryStatusRejected {
			ids = append(ids, memory.MemoryID().String())
		}
	}
	return ids
}

func TestMemoryHygieneApply_RejectsLowQualityCandidate(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	noisyCandidate := candidateSummary(t, "mem-noise", scope, "+def _required_env(name):", domtypes.MemorySourceExtracted, now)
	durableAccepted := acceptedSummaryAt(t, "mem-keep", scope, "Always run go test before merging", now)
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{noisyCandidate, durableAccepted},
	}
	repo := newCandidateApplyMemoryRepository(t, scope, map[string]string{
		"mem-noise": "+def _required_env(name):",
	})
	sut := usecase.NewMemoryUsecase(repo, query, nil)

	result, err := sut.Apply(context.Background(), apptypes.MemoryHygieneApplyCriteria{
		MemoryIDs: []string{"mem-noise"},
		Now:       now,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("Apply failures = %+v, want none", result.Failures)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("Applied entries = %d, want 1", len(result.Applied))
	}
	if result.Applied[0].Transition != "reject" {
		t.Fatalf("Transition = %q, want reject", result.Applied[0].Transition)
	}
	if result.Applied[0].Kind != apptypes.MemoryHygieneSuggestionLowQualityCandidate {
		t.Fatalf("Kind = %q, want low_quality_candidate", result.Applied[0].Kind)
	}
	rejected := repo.rejectedIDs()
	if len(rejected) != 1 || rejected[0] != "mem-noise" {
		t.Fatalf("rejected ids = %v, want [mem-noise]", rejected)
	}
}

func TestMemoryHygieneApply_AcceptedMemoriesUntouchedByCandidatePath(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// An accepted memory whose fact happens to look like noise must not
	// trip the candidate cleanup path. The candidate scan filters by
	// status=candidate, so even when an operator passes the accepted
	// id to apply the re-scan does not produce a suggestion for it.
	noisyAccepted := acceptedSummaryAt(t, "mem-accepted-noise", scope, "+def _required_env(name):", now)
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{noisyAccepted},
	}
	repo := newCandidateApplyMemoryRepository(t, scope, map[string]string{})
	sut := usecase.NewMemoryUsecase(repo, query, nil)

	result, err := sut.Apply(context.Background(), apptypes.MemoryHygieneApplyCriteria{
		MemoryIDs: []string{"mem-accepted-noise"},
		Now:       now,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %+v, want none (accepted row must not be touched by candidate cleanup)", result.Applied)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("Failures = %+v, want one (id without a current suggestion)", result.Failures)
	}
	if rejected := repo.rejectedIDs(); len(rejected) != 0 {
		t.Fatalf("rejected ids = %v, want none — accepted memories must never go through the candidate apply path", rejected)
	}
}

func TestMemoryHygieneApply_HiddenCandidateRequiresOptIn(t *testing.T) {
	t.Parallel()

	scope := workspaceScope(t, "github.com/example/repo")
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	hiddenNoisy := candidateSummary(t, "mem-hidden", scope, "+def helper():", domtypes.MemorySourceExtractedHidden, now)
	query := &stubMemoryQueryService{
		summaries: []apptypes.MemorySummary{hiddenNoisy},
	}
	repo := newCandidateApplyMemoryRepository(t, scope, map[string]string{
		"mem-hidden": "+def helper():",
	})
	// Override the seeded source so the repo serves a hidden candidate
	// the apply path should reject only when IncludeHiddenCandidates is
	// set. NewMemoryCandidate hard-codes MemorySourceExtracted so we
	// rebuild the candidate using the hidden source explicitly.
	evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindFile, "/tmp/MEMORY.md#L1-L1")
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	hiddenMemory, err := model.NewMemoryCandidate(
		domtypes.MemoryID("mem-hidden"),
		domtypes.MemoryTypePreference,
		scope,
		"+def helper():",
		domtypes.MemorySourceExtractedHidden,
		[]domtypes.EvidenceRef{evidence},
		nil,
		domtypes.None[domtypes.MemoryID](),
	)
	if err != nil {
		t.Fatalf("NewMemoryCandidate hidden: %v", err)
	}
	repo.candidates["mem-hidden"] = hiddenMemory
	sut := usecase.NewMemoryUsecase(repo, query, nil)

	withoutOptIn, err := sut.Apply(context.Background(), apptypes.MemoryHygieneApplyCriteria{
		MemoryIDs: []string{"mem-hidden"},
		Now:       now,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(withoutOptIn.Applied) != 0 {
		t.Fatalf("Applied without IncludeHiddenCandidates = %+v, want none", withoutOptIn.Applied)
	}
	if len(withoutOptIn.Failures) != 1 {
		t.Fatalf("Failures without IncludeHiddenCandidates = %+v, want one", withoutOptIn.Failures)
	}

	withOptIn, err := sut.Apply(context.Background(), apptypes.MemoryHygieneApplyCriteria{
		MemoryIDs:               []string{"mem-hidden"},
		Now:                     now,
		IncludeHiddenCandidates: true,
	})
	if err != nil {
		t.Fatalf("Apply with IncludeHiddenCandidates: %v", err)
	}
	if len(withOptIn.Applied) != 1 {
		t.Fatalf("Applied with IncludeHiddenCandidates = %+v, want one", withOptIn.Applied)
	}
	if withOptIn.Applied[0].Transition != "reject" {
		t.Fatalf("Transition = %q, want reject", withOptIn.Applied[0].Transition)
	}
}
