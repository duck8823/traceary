package usecase

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestIsDroppableExtractionFragment(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		reasons []string
		want    bool
	}{
		{name: "diff fragment is droppable", reasons: []string{extractionNoiseDiffFragment}, want: true},
		{name: "generated code is droppable", reasons: []string{extractionNoiseGeneratedCode}, want: true},
		{name: "diff among other reasons is droppable", reasons: []string{extractionNoiseStandaloneCommand, extractionNoiseDiffFragment}, want: true},
		{name: "standalone command is not droppable", reasons: []string{extractionNoiseStandaloneCommand}, want: false},
		{name: "review conclusion is not droppable", reasons: []string{extractionNoiseReviewConclusion}, want: false},
		{name: "work declaration is not droppable", reasons: []string{extractionNoiseWorkDeclaration}, want: false},
		{name: "no reasons is not droppable", reasons: nil, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isDroppableExtractionFragment(tc.reasons); got != tc.want {
				t.Fatalf("isDroppableExtractionFragment(%v) = %v, want %v", tc.reasons, got, tc.want)
			}
		})
	}
}

func TestSummarizeCandidateHygiene(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	staleThreshold := 14 * 24 * time.Hour
	fresh := now.Add(-time.Hour)
	old := now.Add(-30 * 24 * time.Hour)

	candidate := func(t *testing.T, id, fact string, source domtypes.MemorySource, updatedAt time.Time) apptypes.MemorySummary {
		t.Helper()
		return mustHygieneSummary(t, id, fact, domtypes.MemoryStatusCandidate, source, updatedAt)
	}

	cases := []struct {
		name      string
		summaries []apptypes.MemorySummary
		want      apptypes.CandidateHygieneCounts
	}{
		{
			name:      "empty input is all zero",
			summaries: nil,
			want:      apptypes.CandidateHygieneCounts{},
		},
		{
			name: "clean fresh candidate is likely actionable",
			summaries: []apptypes.MemorySummary{
				candidate(t, "m1", "Always run go test before committing", domtypes.MemorySourceExtracted, fresh),
			},
			want: apptypes.CandidateHygieneCounts{LikelyActionable: 1},
		},
		{
			name: "stale candidate is flagged stale only",
			summaries: []apptypes.MemorySummary{
				candidate(t, "m1", "Prefer xerrors.Errorf for wrapping in this repo", domtypes.MemorySourceExtracted, old),
			},
			want: apptypes.CandidateHygieneCounts{Stale: 1},
		},
		{
			name: "exact duplicate facts both count as duplicate",
			summaries: []apptypes.MemorySummary{
				candidate(t, "m1", "Use cmp.Diff for assertions", domtypes.MemorySourceExtracted, fresh),
				candidate(t, "m2", "Use cmp.Diff for assertions", domtypes.MemorySourceExtracted, fresh),
			},
			want: apptypes.CandidateHygieneCounts{Duplicate: 2},
		},
		{
			name: "diff fragment candidate is fragment-like",
			summaries: []apptypes.MemorySummary{
				candidate(t, "m1", "+func handler(ctx context.Context) {", domtypes.MemorySourceExtracted, fresh),
			},
			want: apptypes.CandidateHygieneCounts{FragmentLike: 1},
		},
		{
			name: "extracted-hidden source counts as extracted-hidden",
			summaries: []apptypes.MemorySummary{
				candidate(t, "m1", "go test ./...", domtypes.MemorySourceExtractedHidden, fresh),
			},
			want: apptypes.CandidateHygieneCounts{ExtractedHidden: 1},
		},
		{
			name: "overlapping flags do not double as actionable",
			summaries: []apptypes.MemorySummary{
				// old AND diff-fragment: stale + fragment, never likely-actionable
				candidate(t, "m1", "-old_helper()", domtypes.MemorySourceExtracted, old),
			},
			want: apptypes.CandidateHygieneCounts{Stale: 1, FragmentLike: 1},
		},
		{
			name: "accepted memories are ignored",
			summaries: []apptypes.MemorySummary{
				mustHygieneSummary(t, "m1", "an accepted memory", domtypes.MemoryStatusAccepted, domtypes.MemorySourceManual, old),
				candidate(t, "m2", "a fresh clean candidate note", domtypes.MemorySourceExtracted, fresh),
			},
			want: apptypes.CandidateHygieneCounts{LikelyActionable: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SummarizeCandidateHygiene(tc.summaries, now, staleThreshold)
			if got != tc.want {
				t.Fatalf("SummarizeCandidateHygiene() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func mustHygieneSummary(t *testing.T, id, fact string, status domtypes.MemoryStatus, source domtypes.MemorySource, updatedAt time.Time) apptypes.MemorySummary {
	t.Helper()
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypeLesson,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("github.com/duck8823/traceary")),
		fact,
		status,
		domtypes.ConfidenceLow,
		source,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		updatedAt,
		domtypes.None[time.Time](),
		updatedAt,
		updatedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	return summary
}
