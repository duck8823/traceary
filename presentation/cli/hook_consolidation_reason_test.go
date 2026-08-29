package cli

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func TestFormatConsolidationReason(t *testing.T) {
	tests := []struct {
		name      string
		result    usecase.ConsolidationPressureResult
		atEventID types.Optional[types.EventID]
		want      string
	}{
		{
			name: "covers-to is filled from the latest event id",
			result: usecase.ConsolidationPressureResult{
				Commands: 20,
			},
			atEventID: types.Some(types.EventID("evt-9")),
			want: "[Traceary] Session sess-1 has 20 audited commands of unrefined material since the last refinement. Use the traceary-session-refine skill: say why the work was undertaken and what changed; how it went is optional.\n" +
				"Run: traceary session refine sess-1 --covers-to evt-9 --summary \"<why + what changed>\" --produced-by agent",
		},
		{
			name: "with previous summary",
			result: usecase.ConsolidationPressureResult{
				Commands:         20,
				PreviousSummary:  types.Some("previous summary"),
				PreviousCoversTo: types.Some(types.EventID("evt-1")),
			},
			atEventID: types.Some(types.EventID("evt-9")),
			want: "[Traceary] Session sess-1 has 20 audited commands of unrefined material since the last refinement. Use the traceary-session-refine skill: say why the work was undertaken and what changed; how it went is optional.\n" +
				"Run: traceary session refine sess-1 --covers-to evt-9 --summary \"<why + what changed>\" --produced-by agent\n" +
				"Merge with the previous summary (covers_to=evt-1) rather than rewriting it: previous summary",
		},
		{
			name: "missing latest event id degrades to the placeholder",
			result: usecase.ConsolidationPressureResult{
				Commands: 20,
			},
			atEventID: types.None[types.EventID](),
			want: "[Traceary] Session sess-1 has 20 audited commands of unrefined material since the last refinement. Use the traceary-session-refine skill: say why the work was undertaken and what changed; how it went is optional.\n" +
				"Run: traceary session refine sess-1 --covers-to <event-id> --summary \"<why + what changed>\" --produced-by agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatConsolidationReason(types.SessionID("sess-1"), tt.result, tt.atEventID)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("formatConsolidationReason() mismatch (-want +got):\n%s", diff)
			}
			if n := strings.Count(got, "\n"); n > 2 {
				t.Errorf("newlines = %d, want <= 2", n)
			}
			if strings.Contains(got, "\x1b") {
				t.Errorf("reason contains ANSI: %q", got)
			}
			for _, sub := range []string{
				"traceary-session-refine",
				"traceary session refine ",
				"--produced-by agent",
				"--covers-to ",
			} {
				if !strings.Contains(got, sub) {
					t.Errorf("reason %q does not contain %q", got, sub)
				}
			}
		})
	}
}
