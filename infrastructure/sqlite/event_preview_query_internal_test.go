package sqlite

import (
	"strings"
	"testing"
)

func TestEventPreviewQuery_SelectsOnlyBoundedBodyPrefix(t *testing.T) {
	t.Parallel()
	normalized := strings.ToLower(strings.Join(strings.Fields(selectRecentCommandPreviewsQuery), " "))
	if !strings.Contains(normalized, "substr(e.body, 1, ?)") {
		t.Fatalf("preview query must select a bounded body prefix: %s", normalized)
	}
	if strings.Contains(normalized, "select e.body,") || strings.Contains(normalized, ", e.body,") {
		t.Fatalf("preview query selects an unbounded body column: %s", normalized)
	}
	for _, forbidden := range []string{"command_text", "input_text", "output_text", "body_blocks"} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("preview query selects forbidden payload column %q: %s", forbidden, normalized)
		}
	}
}
