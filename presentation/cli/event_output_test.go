package cli

import (
	"errors"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

var errMetadataOutput = errors.New("metadata output failed")

type metadataFailingWriter struct{}

func (metadataFailingWriter) Write([]byte) (int, error) { return 0, errMetadataOutput }

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

func TestWriteEventMetadataByFormatWrapsOutputErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata []apptypes.EventMetadata
		wantText string
	}{
		{name: "empty list", wantText: "failed to print empty list message"},
		{name: "metadata row", metadata: []apptypes.EventMetadata{{}}, wantText: "failed to print event row"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := writeEventMetadataByFormat(metadataFailingWriter{}, tt.metadata, false, eventTextFormatOptions{fields: []readFieldID{readFieldKind}})
			if !errors.Is(err, errMetadataOutput) {
				t.Fatalf("error = %v, want wrapped output error", err)
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("error = %v, want context %q", err, tt.wantText)
			}
		})
	}
}
