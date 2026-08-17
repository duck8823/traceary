package cli

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func TestFormatConsolidationReason(t *testing.T) {
	tests := []struct {
		name   string
		result usecase.ConsolidationPressureResult
		want   string
	}{
		{
			name: "without previous summary",
			result: usecase.ConsolidationPressureResult{
				PressureBytes: 66560,
			},
			want: "[Traceary] Session sess-1 has unrefined material at or above the consolidation threshold (66560 bytes). Write a session refinement with `traceary session refine` covering this session's events so far. State why the work was undertaken and what changed; if useful, include how it went, including approaches tried and rejected.",
		},
		{
			name: "with previous summary",
			result: usecase.ConsolidationPressureResult{
				PressureBytes:    66560,
				PreviousSummary:  types.Some("previous summary"),
				PreviousCoversTo: types.Some(types.EventID("evt-1")),
			},
			want: "[Traceary] Session sess-1 has unrefined material at or above the consolidation threshold (66560 bytes). Write a session refinement with `traceary session refine` covering this session's events so far. State why the work was undertaken and what changed; if useful, include how it went, including approaches tried and rejected. Merge with the previous summary (covers_to=evt-1): previous summary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatConsolidationReason(types.SessionID("sess-1"), tt.result)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("formatConsolidationReason() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
