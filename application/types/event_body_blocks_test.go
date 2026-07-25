package types

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExtractPlainBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "empty body",
			body: "",
			want: "",
		},
		{
			name: "legacy plain text",
			body: "hello world",
			want: "hello world",
		},
		{
			name: "non-envelope JSON is preserved verbatim",
			body: `{"foo":"bar"}`,
			want: `{"foo":"bar"}`,
		},
		{
			name: "malformed JSON is preserved verbatim",
			body: `{not json`,
			want: `{not json`,
		},
		{
			name: "envelope with only thinking collapses to empty",
			body: `{"blocks":[{"type":"thinking","text":"reasoning"}]}`,
			want: "",
		},
		{
			name: "envelope with text blocks joins with blank line",
			body: `{"blocks":[{"type":"thinking","text":"hidden"},{"type":"text","text":"first"},{"type":"text","text":"second"}]}`,
			want: "first\n\nsecond",
		},
		{
			name: "canonical unknown block does not expose adjacent thinking",
			body: `{"blocks":[{"type":"thinking","text":"hidden-tool-reasoning"},{"type":"tool_use","text":"","id":"call-1"},{"type":"text","text":"visible response"}]}`,
			want: "visible response",
		},
		{
			name: "envelope with empty blocks array collapses to empty",
			body: `{"blocks":[]}`,
			want: "",
		},
		{
			name: "capital-B Blocks is not treated as envelope",
			body: `{"Blocks":[{"type":"text","text":"hi"}]}`,
			want: `{"Blocks":[{"type":"text","text":"hi"}]}`,
		},
		{
			name: "blocks key with foreign shape elements is not treated as envelope",
			body: `{"blocks":[{"foo":"bar"}]}`,
			want: `{"blocks":[{"foo":"bar"}]}`,
		},
		{
			name: "textless custom block remains foreign JSON",
			body: `{"blocks":[{"type":"custom","payload":"foreign-json-marker"}]}`,
			want: `{"blocks":[{"type":"custom","payload":"foreign-json-marker"}]}`,
		},
		{
			name: "blocks:null is not treated as envelope",
			body: `{"blocks":null}`,
			want: `{"blocks":null}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractPlainBody(tc.body)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractPlainBody(%q) mismatch (-want +got):\n%s", tc.body, diff)
			}
		})
	}
}

func TestDecodeCanonicalEnvelope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		body      string
		wantOK    bool
		wantCount int
	}{
		{"empty body not envelope", "", false, 0},
		{"legacy plain text not envelope", "hello", false, 0},
		{"non-envelope JSON not envelope", `{"foo":"bar"}`, false, 0},
		{"capital-B Blocks is not envelope", `{"Blocks":[{"type":"text","text":"hi"}]}`, false, 0},
		{"blocks:null not envelope", `{"blocks":null}`, false, 0},
		{"element missing type/text not envelope", `{"blocks":[{"foo":"bar"}]}`, false, 0},
		{"textless custom block not envelope", `{"blocks":[{"type":"custom","payload":"foreign-json-marker"}]}`, false, 0},
		{"non-string type not envelope", `{"blocks":[{"type":42,"text":"x"}]}`, false, 0},
		{"empty blocks array is envelope", `{"blocks":[]}`, true, 0},
		{"canonical envelope returns blocks", `{"blocks":[{"type":"thinking","text":"a"},{"type":"text","text":"b"}]}`, true, 2},
		{"canonical unknown block carries empty text", `{"blocks":[{"type":"thinking","text":"a"},{"type":"tool_use","text":"","id":"call-1"},{"type":"text","text":"b"}]}`, true, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := DecodeCanonicalEnvelope(tc.body)
			if ok != tc.wantOK {
				t.Errorf("DecodeCanonicalEnvelope(%q) ok = %v, want %v", tc.body, ok, tc.wantOK)
			}
			if len(got) != tc.wantCount {
				t.Errorf("DecodeCanonicalEnvelope(%q) len = %d, want %d", tc.body, len(got), tc.wantCount)
			}
		})
	}
}

func TestParseEventBodyBlocks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want []EventBodyBlock
	}{
		{
			name: "empty body returns nil",
			body: "",
			want: nil,
		},
		{
			name: "legacy plain text becomes single text block",
			body: "hi",
			want: []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: "hi"}},
		},
		{
			name: "non-envelope JSON becomes single text block carrying raw body",
			body: `{"foo":"bar"}`,
			want: []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: `{"foo":"bar"}`}},
		},
		{
			name: "envelope blocks are returned as-is",
			body: `{"blocks":[{"type":"text","text":"hi"}]}`,
			want: []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: "hi"}},
		},
		{
			name: "canonical unknown block is preserved",
			body: `{"blocks":[{"type":"thinking","text":"hidden"},{"type":"tool_use","text":"","id":"call-1"},{"type":"text","text":"visible"}]}`,
			want: []EventBodyBlock{
				{Type: EventBodyBlockTypeThinking, Text: "hidden"},
				{Type: EventBodyBlockType("tool_use"), Text: ""},
				{Type: EventBodyBlockTypeText, Text: "visible"},
			},
		},
		{
			name: "capital-B key falls back to legacy single text block",
			body: `{"Blocks":[{"type":"text","text":"hi"}]}`,
			want: []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: `{"Blocks":[{"type":"text","text":"hi"}]}`}},
		},
		{
			name: "non-block-shaped element falls back to legacy single text block",
			body: `{"blocks":[{"foo":"bar"}]}`,
			want: []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: `{"blocks":[{"foo":"bar"}]}`}},
		},
		{
			name: "textless custom block falls back to raw body",
			body: `{"blocks":[{"type":"custom","payload":"foreign-json-marker"}]}`,
			want: []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: `{"blocks":[{"type":"custom","payload":"foreign-json-marker"}]}`}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseEventBodyBlocks(tc.body)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ParseEventBodyBlocks(%q) mismatch (-want +got):\n%s", tc.body, diff)
			}
		})
	}
}
