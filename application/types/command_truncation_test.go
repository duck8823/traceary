package types

import (
	"strings"
	"testing"
)

func TestTruncateCommandPayload(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		body              string
		limit             int
		wantBody          string
		wantTruncated     bool
		wantOriginalRunes int
		wantOriginalBytes int
	}{
		"empty body keeps zero counts": {
			body:              "",
			limit:             10,
			wantBody:          "",
			wantTruncated:     false,
			wantOriginalRunes: 0,
			wantOriginalBytes: 0,
		},
		"short payload is returned untouched": {
			body:              "go test ./...",
			limit:             50,
			wantBody:          "go test ./...",
			wantTruncated:     false,
			wantOriginalRunes: 13,
			wantOriginalBytes: 13,
		},
		"boundary length matches limit and is not truncated": {
			body:              strings.Repeat("a", 60),
			limit:             60,
			wantBody:          strings.Repeat("a", 60),
			wantTruncated:     false,
			wantOriginalRunes: 60,
			wantOriginalBytes: 60,
		},
		"one rune over limit is truncated with ellipsis": {
			body:              strings.Repeat("a", 61),
			limit:             60,
			wantBody:          strings.Repeat("a", 60) + TruncationEllipsis,
			wantTruncated:     true,
			wantOriginalRunes: 61,
			wantOriginalBytes: 61,
		},
		"multibyte runes count by rune not byte": {
			body:              strings.Repeat("あ", 5),
			limit:             3,
			wantBody:          strings.Repeat("あ", 3) + TruncationEllipsis,
			wantTruncated:     true,
			wantOriginalRunes: 5,
			wantOriginalBytes: len(strings.Repeat("あ", 5)),
		},
		"non-positive limit disables truncation": {
			body:              strings.Repeat("a", 200),
			limit:             0,
			wantBody:          strings.Repeat("a", 200),
			wantTruncated:     false,
			wantOriginalRunes: 200,
			wantOriginalBytes: 200,
		},
		"negative limit disables truncation": {
			body:              strings.Repeat("a", 200),
			limit:             -5,
			wantBody:          strings.Repeat("a", 200),
			wantTruncated:     false,
			wantOriginalRunes: 200,
			wantOriginalBytes: 200,
		},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := TruncateCommandPayload(tc.body, tc.limit)
			if got.Body != tc.wantBody {
				t.Errorf("Body = %q, want %q", got.Body, tc.wantBody)
			}
			if got.Truncated != tc.wantTruncated {
				t.Errorf("Truncated = %v, want %v", got.Truncated, tc.wantTruncated)
			}
			if got.OriginalRuneCount != tc.wantOriginalRunes {
				t.Errorf("OriginalRuneCount = %d, want %d", got.OriginalRuneCount, tc.wantOriginalRunes)
			}
			if got.OriginalByteCount != tc.wantOriginalBytes {
				t.Errorf("OriginalByteCount = %d, want %d", got.OriginalByteCount, tc.wantOriginalBytes)
			}
		})
	}
}
