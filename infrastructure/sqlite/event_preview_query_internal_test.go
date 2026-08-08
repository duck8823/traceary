package sqlite

import (
	"strings"
	"testing"
)

func TestEventPreviewQuery_SelectsBoundedCommandPrefix(t *testing.T) {
	t.Parallel()
	normalized := strings.ToLower(strings.Join(strings.Fields(selectRecentCommandPreviewsQuery), " "))
	if !strings.Contains(normalized, "substr(coalesce(a.command_text, e.body), 1, ?)") {
		t.Fatalf("preview query must select a bounded command_text prefix with body fallback: %s", normalized)
	}
	if !strings.Contains(normalized, "from event_metadata_projection m") || !strings.Contains(normalized, "join events e on e.id = selected.id") {
		t.Fatalf("preview query must select metadata candidates before hydrating bounded bodies: %s", normalized)
	}
	if !strings.Contains(normalized, "left join command_audits a on a.event_id = selected.id") {
		t.Fatalf("preview query must join command_audits for retained command text: %s", normalized)
	}
	if strings.Contains(normalized, "select e.body,") || strings.Contains(normalized, ", e.body,") {
		t.Fatalf("preview query selects an unbounded body column: %s", normalized)
	}
	for _, forbidden := range []string{"input_text", "output_text", "body_blocks"} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("preview query selects forbidden payload column %q: %s", forbidden, normalized)
		}
	}
}
