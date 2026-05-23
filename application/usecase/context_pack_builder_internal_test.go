package usecase

import (
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

// summarizeCommand drives the handoff RECENT_COMMANDS list, which feeds
// both the CLI `traceary session handoff` text rendering and the MCP
// session_handoff tool. The single-line cap is intentionally smaller
// than the list-surface defaults (60 vs 500 runes) because each row
// renders on its own line; truncation here keeps multi-hundred-line
// command_executed bodies from blowing up the handoff output.

func TestSummarizeCommand_TruncatesAtHandoffLimit(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  string
	}{
		"empty input falls back to dash": {
			input: "",
			want:  "-",
		},
		"whitespace-only input falls back to dash": {
			input: "   \t\n  ",
			want:  "-",
		},
		"short command stays untouched": {
			input: "go test ./...",
			want:  "go test ./...",
		},
		"only first paragraph is kept": {
			input: "go test ./...\n\ndetails: build cache hit",
			want:  "go test ./...",
		},
		"runs of whitespace collapse to single spaces": {
			input: "go    test\t./...",
			want:  "go test ./...",
		},
		"boundary length matches limit and is not truncated": {
			input: strings.Repeat("a", apptypes.DefaultHandoffRecentCommandLimit),
			want:  strings.Repeat("a", apptypes.DefaultHandoffRecentCommandLimit),
		},
		"one rune over limit is truncated with ellipsis": {
			input: strings.Repeat("a", apptypes.DefaultHandoffRecentCommandLimit+1),
			want:  strings.Repeat("a", apptypes.DefaultHandoffRecentCommandLimit) + apptypes.TruncationEllipsis,
		},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := summarizeCommand(tc.input); got != tc.want {
				t.Fatalf("summarizeCommand(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
