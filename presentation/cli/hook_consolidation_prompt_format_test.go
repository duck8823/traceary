package cli

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func TestFormatConsolidationContext(t *testing.T) {
	t.Parallel()

	req := consolidationRequest{
		SessionID: "sess-1",
		Result:    usecase.ConsolidationPressureResult{Commands: 20},
		AtEventID: types.Some(types.EventID("evt-9")),
	}
	got := formatConsolidationContext(req)
	want := "[Traceary] Session sess-1 has 20 audited commands of unrefined material. Before you start this turn, load the traceary-session-refine skill and record what happened.\n" +
		"Run: traceary session refine sess-1 --covers-to evt-9 --summary \"<why + what changed>\" --produced-by agent\n" +
		"Then continue with the user's request."
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("formatConsolidationContext() mismatch (-want +got):\n%s", diff)
	}
	if n := strings.Count(got, "\n"); n != 2 {
		t.Errorf("newlines = %d, want 2 (three lines)", n)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("context contains ANSI: %q", got)
	}
	if strings.Count(got, "traceary session refine ") != 1 {
		t.Errorf("want exactly one refine command line, got %q", got)
	}
	reason := formatConsolidationReason(req.SessionID, req.Result, req.AtEventID)
	if got == reason {
		t.Fatal("prompt-time wording must be distinct from the Stop reason")
	}

	t.Run("missing latest event id degrades to the placeholder", func(t *testing.T) {
		t.Parallel()
		missing := consolidationRequest{
			SessionID: "sess-1",
			Result:    usecase.ConsolidationPressureResult{Commands: 20},
			AtEventID: types.None[types.EventID](),
		}
		got := formatConsolidationContext(missing)
		if !strings.Contains(got, "--covers-to <event-id>") {
			t.Fatalf("covers-to fallback missing: %q", got)
		}
	})
}
