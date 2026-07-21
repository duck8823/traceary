package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// EventProjection selects how much stored event content a read surface may
// materialize. The zero value preserves each presentation adapter's legacy
// body-limit behavior.
type EventProjection string

const (
	// EventProjectionLegacy delegates projection selection to compatibility
	// flags such as body_limit and full_body.
	EventProjectionLegacy EventProjection = ""
	// EventProjectionMetadata returns event metadata without stored body text.
	EventProjectionMetadata EventProjection = "metadata"
	// EventProjectionBounded returns stored body text under a response limit.
	EventProjectionBounded EventProjection = "bounded"
	// EventProjectionFull returns the full stored body.
	EventProjectionFull EventProjection = "full"
)

// EventProjectionFrom parses an optional public projection name.
func EventProjectionFrom(value string) (EventProjection, error) {
	projection := EventProjection(strings.TrimSpace(value))
	switch projection {
	case EventProjectionLegacy, EventProjectionMetadata, EventProjectionBounded, EventProjectionFull:
		return projection, nil
	default:
		return EventProjectionLegacy, xerrors.Errorf("unsupported event projection %q (supported: metadata, bounded, full)", value)
	}
}
