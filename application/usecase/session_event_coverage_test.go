package usecase_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func TestSummarizeSessionEventCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		events    []usecase.EventCoverageInput
		want      usecase.SessionEventCoverage
		wantRatio float64
	}{
		{
			name:      "empty input yields zero counts and zero ratio",
			events:    nil,
			want:      usecase.SessionEventCoverage{},
			wantRatio: 0,
		},
		{
			name: "session without observed start is excluded as truncated",
			events: []usecase.EventCoverageInput{
				{SessionID: "s1", Kind: types.EventKindPrompt},
				{SessionID: "s1", Kind: types.EventKindTranscript},
			},
			want:      usecase.SessionEventCoverage{},
			wantRatio: 0,
		},
		{
			name: "boundary only session is counted but not enriched",
			events: []usecase.EventCoverageInput{
				{SessionID: "s1", Kind: types.EventKindSessionStarted},
				{SessionID: "s1", Kind: types.EventKindSessionEnded},
			},
			want: usecase.SessionEventCoverage{
				Sessions:     1,
				BoundaryOnly: 1,
			},
			wantRatio: 1,
		},
		{
			name: "prompt only counts as enriched with prompt",
			events: []usecase.EventCoverageInput{
				{SessionID: "s1", Kind: types.EventKindSessionStarted},
				{SessionID: "s1", Kind: types.EventKindPrompt},
			},
			want: usecase.SessionEventCoverage{
				Sessions:   1,
				Enriched:   1,
				WithPrompt: 1,
			},
			wantRatio: 0,
		},
		{
			name: "transcript only counts as enriched with transcript",
			events: []usecase.EventCoverageInput{
				{SessionID: "s1", Kind: types.EventKindSessionStarted},
				{SessionID: "s1", Kind: types.EventKindTranscript},
			},
			want: usecase.SessionEventCoverage{
				Sessions:       1,
				Enriched:       1,
				WithTranscript: 1,
			},
		},
		{
			name: "command only counts as enriched with command",
			events: []usecase.EventCoverageInput{
				{SessionID: "s1", Kind: types.EventKindSessionStarted},
				{SessionID: "s1", Kind: types.EventKindCommandExecuted},
			},
			want: usecase.SessionEventCoverage{
				Sessions:    1,
				Enriched:    1,
				WithCommand: 1,
			},
		},
		{
			name: "neutral events do not enrich a boundary only session",
			events: []usecase.EventCoverageInput{
				{SessionID: "s1", Kind: types.EventKindSessionStarted},
				{SessionID: "s1", Kind: types.EventKindCompactSummary},
				{SessionID: "s1", Kind: types.EventKindNote},
			},
			want: usecase.SessionEventCoverage{
				Sessions:     1,
				BoundaryOnly: 1,
			},
			wantRatio: 1,
		},
		{
			name: "mixed sessions report ratio",
			events: []usecase.EventCoverageInput{
				{SessionID: "boundary-a", Kind: types.EventKindSessionStarted},
				{SessionID: "boundary-a", Kind: types.EventKindSessionEnded},
				{SessionID: "boundary-b", Kind: types.EventKindSessionStarted},
				{SessionID: "enriched-a", Kind: types.EventKindSessionStarted},
				{SessionID: "enriched-a", Kind: types.EventKindPrompt},
				{SessionID: "enriched-a", Kind: types.EventKindTranscript},
				{SessionID: "enriched-a", Kind: types.EventKindCommandExecuted},
				{SessionID: "truncated", Kind: types.EventKindPrompt},
			},
			want: usecase.SessionEventCoverage{
				Sessions:       3,
				BoundaryOnly:   2,
				Enriched:       1,
				WithPrompt:     1,
				WithTranscript: 1,
				WithCommand:    1,
			},
			wantRatio: float64(2) / float64(3),
		},
		{
			name: "events with empty session id are ignored",
			events: []usecase.EventCoverageInput{
				{SessionID: "", Kind: types.EventKindSessionStarted},
				{SessionID: "", Kind: types.EventKindPrompt},
			},
			want: usecase.SessionEventCoverage{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := usecase.SummarizeSessionEventCoverage(tt.events)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("SummarizeSessionEventCoverage() mismatch (-want +got):\n%s", diff)
			}
			if gotRatio := got.BoundaryOnlyRatio(); gotRatio != tt.wantRatio {
				t.Fatalf("BoundaryOnlyRatio() = %v, want %v", gotRatio, tt.wantRatio)
			}
		})
	}
}
