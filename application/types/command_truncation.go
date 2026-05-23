package types

import (
	"unicode/utf8"
)

// Operator-facing recent-command renderers share the same rune-based
// truncation policy so a single noisy command_executed payload does not
// dominate either a terminal pane or an MCP context-window response.
// Full content is preserved in the database and remains retrievable
// through the explicit event detail / show surfaces, which bypass these
// limits.
const (
	// DefaultListEventBodyLimit caps the per-event body projection on
	// MCP list-style surfaces (list_events / search / get_context).
	// Callers pass body_limit=0 / full_body=true to opt out. The 500-
	// rune budget matches the historical default introduced in #799.
	DefaultListEventBodyLimit = 500

	// DefaultTopSnapshotBodyLimit caps recent-command and recent-failure
	// rows on `traceary top --snapshot --json` so a multi-hundred-line
	// command_executed payload does not balloon the script-friendly
	// snapshot output. The text snapshot keeps using truncateMessage for
	// tabular alignment.
	DefaultTopSnapshotBodyLimit = 500

	// DefaultHandoffRecentCommandLimit is the per-line summary cap used
	// for the handoff RECENT_COMMANDS list. The output is single-line
	// per row, so the budget is intentionally smaller than the list
	// surfaces above.
	DefaultHandoffRecentCommandLimit = 60

	// TruncationEllipsis is the marker appended to a truncated payload.
	// All operator-facing renderers use the same glyph so consumers can
	// detect truncation textually if they ignore the structured flag.
	TruncationEllipsis = "…"
)

// CommandPayloadTruncation carries the result of applying the shared
// truncation policy to a recent-command body. Callers attach Truncated
// and OriginalRuneCount / OriginalByteCount to JSON shapes so consumers
// can tell that the value has been cut and can fetch the full event via
// explicit detail lookups.
type CommandPayloadTruncation struct {
	Body              string
	Truncated         bool
	OriginalRuneCount int
	OriginalByteCount int
}

// TruncateCommandPayload applies the shared rune-based truncation
// policy. A non-positive limit disables truncation entirely; the
// original body is returned with Truncated=false but the original
// counts are still populated so callers can record size metadata even
// when no cut occurred.
func TruncateCommandPayload(body string, limit int) CommandPayloadTruncation {
	originalRunes := utf8.RuneCountInString(body)
	originalBytes := len(body)
	if limit <= 0 || originalRunes <= limit {
		return CommandPayloadTruncation{
			Body:              body,
			Truncated:         false,
			OriginalRuneCount: originalRunes,
			OriginalByteCount: originalBytes,
		}
	}
	runes := []rune(body)
	return CommandPayloadTruncation{
		Body:              string(runes[:limit]) + TruncationEllipsis,
		Truncated:         true,
		OriginalRuneCount: originalRunes,
		OriginalByteCount: originalBytes,
	}
}
