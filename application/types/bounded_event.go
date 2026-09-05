package types

import (
	"unicode/utf8"

	"golang.org/x/xerrors"
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
	canonicalEnvelope bool
	bodyBlocks        []EventBodyBlock
}

// BoundedEventOf creates a bounded event projection.
func BoundedEventOf(
	metadata EventMetadata,
	body string,
	bodyRuneLimit int,
	visibleBodyRunes int,
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
	return BoundedEvent{
		metadata:          metadata,
		body:              body,
		bodyRuneLimit:     bodyRuneLimit,
		visibleBodyRunes:  visibleBodyRunes,
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

// BodyResponseTruncated reports whether the current projection omitted visible
// body runes. It is independent from ingest/storage truncation.
func (e BoundedEvent) BodyResponseTruncated() bool {
	return utf8.RuneCountInString(e.body) < e.visibleBodyRunes
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
// Full blocks are permitted only when the visible-text response is
// untruncated, so blocks cannot bypass the bounded response contract.
func (e BoundedEvent) WithCanonicalBodyBlocks(blocks []EventBodyBlock) (BoundedEvent, error) {
	if !e.canonicalEnvelope {
		return BoundedEvent{}, xerrors.Errorf("cannot attach blocks to a non-canonical body")
	}
	if e.BodyResponseTruncated() {
		return BoundedEvent{}, xerrors.Errorf("cannot attach blocks to a response-truncated body")
	}
	e.bodyBlocks = append([]EventBodyBlock(nil), blocks...)
	return e, nil
}
