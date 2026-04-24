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
			name: "capital-B key falls back to legacy single text block",
			body: `{"Blocks":[{"type":"text","text":"hi"}]}`,
			want: []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: `{"Blocks":[{"type":"text","text":"hi"}]}`}},
		},
		{
			name: "non-block-shaped element falls back to legacy single text block",
			body: `{"blocks":[{"foo":"bar"}]}`,
			want: []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: `{"blocks":[{"foo":"bar"}]}`}},
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
