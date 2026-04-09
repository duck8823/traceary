package cli

import (
	"strings"
	"testing"
)

func TestTruncateMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "短いメッセージはそのまま返す",
			in:   "hello world",
			want: "hello world",
		},
		{
			name: "改行をスペースに正規化する",
			in:   "line1\nline2\nline3",
			want: "line1 line2 line3",
		},
		{
			name: "連続空白を1つに正規化する",
			in:   "hello   \t  world",
			want: "hello world",
		},
		{
			name: "80文字を超えるメッセージを切り詰める",
			in:   strings.Repeat("a", 100),
			want: strings.Repeat("a", 80) + "…",
		},
		{
			name: "ちょうど80文字はそのまま返す",
			in:   strings.Repeat("b", 80),
			want: strings.Repeat("b", 80),
		},
		{
			name: "マルチバイト文字をルーン単位で切り詰める",
			in:   strings.Repeat("あ", 100),
			want: strings.Repeat("あ", 80) + "…",
		},
		{
			name: "空文字列はそのまま返す",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := truncateMessage(tt.in)
			if got != tt.want {
				t.Errorf("truncateMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}
