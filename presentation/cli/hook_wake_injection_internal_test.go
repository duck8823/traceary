package cli

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/types"
)

func TestFormatWakeInjectionText(t *testing.T) {
	t.Parallel()

	newest := queryservice.SessionWakeSummary{
		SessionID: "s3",
		Summary:   "summary three",
	}
	middle := queryservice.SessionWakeSummary{
		SessionID: "s2",
		Summary:   "summary two",
	}
	oldest := queryservice.SessionWakeSummary{
		SessionID: "s1",
		Summary:   "summary one",
	}
	three := []queryservice.SessionWakeSummary{newest, middle, oldest}

	// Precompute sizes for budget edges.
	// Format: header\n + s3\n + \n + s2\n + \n + s1\n
	allText := formatWakeInjectionText(three, 1<<30)
	twoText := formatWakeInjectionText([]queryservice.SessionWakeSummary{newest, middle}, 1<<30)
	oneText := formatWakeInjectionText([]queryservice.SessionWakeSummary{newest}, 1<<30)

	tests := []struct {
		name      string
		summaries []queryservice.SessionWakeSummary
		budget    int64
		want      string
	}{
		{
			name:      "three eligible summaries produce header and all three newest first",
			summaries: three,
			budget:    8192,
			want:      allText,
		},
		{
			name:      "zero eligible summaries yield empty string",
			summaries: nil,
			budget:    8192,
			want:      "",
		},
		{
			name: "budget zero disables injection",
			summaries: []queryservice.SessionWakeSummary{
				{SessionID: "s", Summary: "tiny"},
			},
			budget: 0,
			want:   "",
		},
		{
			name: "negative budget disables injection",
			summaries: []queryservice.SessionWakeSummary{
				{SessionID: "s", Summary: "tiny"},
			},
			budget: -1,
			want:   "",
		},
		{
			name: "newest alone exceeds budget injects nothing",
			summaries: []queryservice.SessionWakeSummary{
				{SessionID: "big", Summary: strings.Repeat("x", 100)},
			},
			budget: int64(len(application.WakeInjectionHeader)), // too small for header+summary
			want:   "",
		},
		{
			name:      "two fit but three do not yields exactly two",
			summaries: three,
			budget:    int64(len(twoText)),
			want:      twoText,
		},
		{
			name:      "budget of exactly one summary yields that one",
			summaries: three,
			budget:    int64(len(oneText)),
			want:      oneText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatWakeInjectionText(tt.summaries, tt.budget)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("formatWakeInjectionText() mismatch (-want +got):\n%s", diff)
			}
			if tt.budget > 0 && len(got) > int(tt.budget) {
				t.Fatalf("output length %d exceeds budget %d", len(got), tt.budget)
			}
			if strings.Contains(got, "event body") {
				t.Fatalf("output must not contain event body text: %q", got)
			}
			_ = types.SessionID("compile-check")
		})
	}
}

func TestFormatWakeInjectionText_TotalNeverExceedsBudget(t *testing.T) {
	t.Parallel()

	summaries := []queryservice.SessionWakeSummary{
		{SessionID: "a", Summary: strings.Repeat("a", 40)},
		{SessionID: "b", Summary: strings.Repeat("b", 40)},
		{SessionID: "c", Summary: strings.Repeat("c", 40)},
	}
	for budget := int64(0); budget <= 200; budget++ {
		got := formatWakeInjectionText(summaries, budget)
		if int64(len(got)) > budget {
			t.Fatalf("budget=%d output len=%d exceeds budget: %q", budget, len(got), got)
		}
	}
}
