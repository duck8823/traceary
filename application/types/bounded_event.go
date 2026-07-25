package types

import (
	"unicode/utf8"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// BoundedEvent is the read model for event content whose visible-text body was
// capped before crossing the persistence boundary. Persisted body truncation
// belongs to Metadata().BodyExtent(); BodyResponseTruncated reports only the
// current read projection.
type BoundedEvent struct {
	metadata          EventMetadata
	body              string
	bodyRuneLimit     int
	visibleBodyRunes  int
	bodyAvailability  domtypes.BodyAvailability
	canonicalEnvelope bool
	bodyBlocks        []EventBodyBlock
}

// BoundedEventOf creates a bounded event projection.
func BoundedEventOf(
	metadata EventMetadata,
	body string,
	bodyRuneLimit int,
	visibleBodyRunes int,
	bodyAvailability domtypes.BodyAvailability,
	canonicalEnvelope bool,
) (BoundedEvent, error) {
	if bodyRuneLimit <= 0 {
		return BoundedEvent{}, xerrors.Errorf("body rune limit must be greater than or equal to 1")
	}
	if utf8.RuneCountInString(body) > bodyRuneLimit {
		return BoundedEvent{}, xerrors.Errorf("bounded body exceeds its rune limit")
	}
	if visibleBodyRunes < 0 {
		return BoundedEvent{}, xerrors.Errorf("visible body runes must be greater than or equal to 0")
	}
	if utf8.RuneCountInString(body) > visibleBodyRunes {
		return BoundedEvent{}, xerrors.Errorf("bounded body exceeds its visible body length")
	}
	if _, err := domtypes.BodyAvailabilityFrom(bodyAvailability.String()); err != nil {
		return BoundedEvent{}, xerrors.Errorf("invalid body availability: %w", err)
	}
	if !bodyAvailability.IsAvailable() {
		if body != "" || visibleBodyRunes != 0 {
			return BoundedEvent{}, xerrors.Errorf("unavailable body must not expose content or length")
		}
		if canonicalEnvelope {
			return BoundedEvent{}, xerrors.Errorf("unavailable body must not be marked as a canonical envelope")
		}
	}
	return BoundedEvent{
		metadata:          metadata,
		body:              body,
		bodyRuneLimit:     bodyRuneLimit,
		visibleBodyRunes:  visibleBodyRunes,
		bodyAvailability:  bodyAvailability,
		canonicalEnvelope: canonicalEnvelope,
	}, nil
}

// Metadata returns body-free event facts and persisted truncation provenance.
func (e BoundedEvent) Metadata() EventMetadata { return e.metadata }

// Body returns the visible-text prefix without a response ellipsis.
func (e BoundedEvent) Body() string { return e.body }

// BodyRuneLimit returns the maximum body prefix requested from persistence.
func (e BoundedEvent) BodyRuneLimit() int { return e.bodyRuneLimit }

// VisibleBodyRunes returns the complete visible-text rune count before
// response truncation.
func (e BoundedEvent) VisibleBodyRunes() int { return e.visibleBodyRunes }

// BodyAvailability reports whether the raw event body remains available.
func (e BoundedEvent) BodyAvailability() domtypes.BodyAvailability {
	return e.bodyAvailability
}

// BodyResponseTruncated reports whether the current projection omitted visible
// body runes. It is independent from ingest/storage truncation.
func (e BoundedEvent) BodyResponseTruncated() bool {
	return e.bodyAvailability.IsAvailable() &&
		utf8.RuneCountInString(e.body) < e.visibleBodyRunes
}

// CanonicalEnvelope reports whether the stored body was recognized as the
// canonical block envelope by the bounded SQLite projection.
func (e BoundedEvent) CanonicalEnvelope() bool { return e.canonicalEnvelope }

// BodyBlocks returns canonical blocks attached by the list-only compatibility
// fallback. Search and context projections leave it empty.
func (e BoundedEvent) BodyBlocks() []EventBodyBlock {
	return append([]EventBodyBlock(nil), e.bodyBlocks...)
}

// WithCanonicalBodyBlocks attaches an explicitly loaded canonical envelope.
// Full blocks are permitted only when the visible-text response is available
// and untruncated, so blocks cannot bypass the bounded response contract.
func (e BoundedEvent) WithCanonicalBodyBlocks(blocks []EventBodyBlock) (BoundedEvent, error) {
	if !e.bodyAvailability.IsAvailable() {
		return BoundedEvent{}, xerrors.Errorf("cannot attach blocks to an unavailable body")
	}
	if !e.canonicalEnvelope {
		return BoundedEvent{}, xerrors.Errorf("cannot attach blocks to a non-canonical body")
	}
	if e.BodyResponseTruncated() {
		return BoundedEvent{}, xerrors.Errorf("cannot attach blocks to a response-truncated body")
	}
	e.bodyBlocks = append([]EventBodyBlock(nil), blocks...)
	return e, nil
}
