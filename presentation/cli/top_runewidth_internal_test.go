package cli

import (
	"strings"
	"testing"
)

func TestRuneWidth_CountsWideCharsAsTwoColumns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		input    string
		expected int
	}{
		{name: "ascii only", input: "abc", expected: 3},
		{name: "single hiragana", input: "あ", expected: 2},
		{name: "single ideograph", input: "中", expected: 2},
		{name: "single hangul syllable", input: "한", expected: 2},
		{name: "mixed ascii and CJK", input: "go テスト", expected: 9},
		{name: "fullwidth digit", input: "１", expected: 2},
		{name: "halfwidth katakana", input: "ｱ", expected: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := runeWidth(tc.input); got != tc.expected {
				t.Fatalf("runeWidth(%q) = %d, want %d", tc.input, got, tc.expected)
			}
		})
	}
}

func TestCompactTopWorkspace_VisualWidthBudget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		input          string
		exact          string
		mustStartWith  string
		mustEndWith    string
		mustNotOverrun bool
	}{
		{
			name:  "empty becomes dash",
			input: "",
			exact: "-",
		},
		{
			name:  "ascii under budget passes through",
			input: "github.com/owner/repo",
			exact: "github.com/owner/repo",
		},
		{
			// 58 ascii cols, much wider than the budget. Tail stays
			// readable (`/repo`) and an ellipsis is prepended.
			name:           "ascii over budget keeps tail with ellipsis",
			input:          "github.com/very-long-organization-name/another-module/repo",
			mustStartWith:  "…",
			mustEndWith:    "another-module/repo",
			mustNotOverrun: true,
		},
		{
			// CJK runes are wide so 17 ascii + 11 CJK = 39 cols, over
			// the 36-col budget. The tail repo identifier survives.
			name:           "wide chars consume the budget at 2 columns each",
			input:          "github.com/owner/プロジェクトリポジトリ",
			mustStartWith:  "…",
			mustEndWith:    "プロジェクトリポジトリ",
			mustNotOverrun: true,
		},
		{
			// 17 ascii cols + 18 wide chars (36 cols) = 53 cols.
			// Truncate from the head; the trailing 名前 must survive.
			name:           "wide chars over the budget truncate from the head",
			input:          "github.com/owner/とてもとても長い日本語のリポジトリ名前",
			mustStartWith:  "…",
			mustEndWith:    "リポジトリ名前",
			mustNotOverrun: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := compactTopWorkspace(tc.input)
			if tc.exact != "" {
				if got != tc.exact {
					t.Fatalf("compactTopWorkspace(%q) = %q, want %q", tc.input, got, tc.exact)
				}
			}
			if tc.mustStartWith != "" && !strings.HasPrefix(got, tc.mustStartWith) {
				t.Fatalf("compactTopWorkspace(%q) = %q, want prefix %q", tc.input, got, tc.mustStartWith)
			}
			if tc.mustEndWith != "" && !strings.HasSuffix(got, tc.mustEndWith) {
				t.Fatalf("compactTopWorkspace(%q) = %q, want suffix %q", tc.input, got, tc.mustEndWith)
			}
			if tc.mustNotOverrun {
				if width := runeWidth(got); width > topWorkspaceMaxWidth {
					t.Fatalf("compactTopWorkspace output width = %d, want <= %d (output=%q)", width, topWorkspaceMaxWidth, got)
				}
			}
		})
	}
}
