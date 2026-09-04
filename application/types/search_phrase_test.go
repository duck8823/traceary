package types_test

import (
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestSearchPhrase_MatchesFoldedContiguousSubstring(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, needle, haystack string
		want                   bool
	}{
		{
			name:     "multi-word phrase in refinement summary",
			needle:   "keep-alive window",
			haystack: "rotate the keep-alive window weekly",
			want:     true,
		},
		{
			name:     "hyphenated term is not split",
			needle:   "keep-days",
			haystack: "raise keep-days to 14",
			want:     true,
		},
		{
			name:     "token AND would match but phrase does not",
			needle:   "keep-alive window",
			haystack: "keep-alive something window",
			want:     false,
		},
		{
			name:     "ASCII fold on both sides",
			needle:   "NEEDLE",
			haystack: "prefix needle suffix",
			want:     true,
		},
		{
			name:     "percent is a literal",
			needle:   "100%",
			haystack: "rate 100% done",
			want:     true,
		},
		{
			name:     "underscore is a literal",
			needle:   "keep_days",
			haystack: "not keep-days",
			want:     false,
		},
		{
			name:     "underscore matches only underscore",
			needle:   "keep_days",
			haystack: "set keep_days later",
			want:     true,
		},
		{
			name:     "empty needle never matches",
			needle:   "  ",
			haystack: "anything",
			want:     false,
		},
		{
			name:     "no-hit term",
			needle:   "xyzzy-nomatch",
			haystack: "rotate the keep-alive window weekly",
			want:     false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := apptypes.SearchPhraseOf(tc.needle).Matches(tc.haystack)
			if got != tc.want {
				t.Fatalf("Matches(%q, %q) = %v, want %v", tc.needle, tc.haystack, got, tc.want)
			}
		})
	}
}
