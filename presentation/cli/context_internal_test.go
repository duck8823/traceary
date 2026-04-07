package cli

import "testing"

func TestNormalizeGitRemoteURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "https URL を正規化する",
			input: "https://github.com/duck8823/traceary.git",
			want:  "github.com/duck8823/traceary",
		},
		{
			name:  "ssh scp 形式を正規化する",
			input: "git@github.com:duck8823/traceary.git",
			want:  "github.com/duck8823/traceary",
		},
		{
			name:  "ssh URL を正規化する",
			input: "ssh://git@github.com/duck8823/traceary.git",
			want:  "github.com/duck8823/traceary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := normalizeGitRemoteURL(tt.input)
			if got != tt.want {
				t.Fatalf("normalizeGitRemoteURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
