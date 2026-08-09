package sqlite

import (
	"strings"
	"testing"
)

func TestEventPreviewQuery_SelectsMetadataWithoutCompressedCommandText(t *testing.T) {
	t.Parallel()
	normalized := strings.ToLower(strings.Join(strings.Fields(selectRecentCommandPreviewsQuery), " "))
	if !strings.Contains(normalized, "from event_metadata_projection m") || !strings.Contains(normalized, "join events e on e.id = selected.id") {
		t.Fatalf("preview query must select metadata candidates before hydrating bounded bodies: %s", normalized)
	}
	if !strings.Contains(normalized, "left join command_audits a on a.event_id = selected.id") {
		t.Fatalf("preview query must join command_audits for retained command extent: %s", normalized)
	}
	// After payload compression, length(command_text) is the physical size.
	// StoredBytes must prefer the codec plaintext figure.
	if !strings.Contains(normalized, "coalesce(a.command_plaintext_bytes, length(cast(a.command_text as blob)), selected.body_stored_bytes)") {
		t.Fatalf("preview query must prefer command_plaintext_bytes for stored extent: %s", normalized)
	}
	// The outer SELECT must not project a body/command prefix: codec hydration
	// rebuilds the preview text in Go. Inspect only the result list.
	selectIdx := strings.Index(normalized, ") select selected.id,")
	if selectIdx < 0 {
		t.Fatalf("preview query missing outer select: %s", normalized)
	}
	selectList := normalized[selectIdx:]
	if strings.Contains(selectList, "substr(") {
		t.Fatalf("preview query must not project a substr prefix of command_text: %s", selectList)
	}
	if strings.Contains(selectList, "a.command_text,") || strings.Contains(selectList, "coalesce(a.command_text") {
		t.Fatalf("preview query must not project command_text as a result column: %s", selectList)
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
