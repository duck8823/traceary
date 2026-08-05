package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

// SegmentSummaryV1 is the immutable, privacy-preserving summary descriptor.
const SegmentSummaryV1 uint32 = 1

// SegmentSummaryBloomMaxBitsV1 is the shared format/generator/reader cap.
const SegmentSummaryBloomMaxBitsV1 uint32 = 8 << 20

// SummaryTokenKind identifies a fixed metadata dimension. Values are HMACs;
// plaintext metadata is deliberately not representable by this API.
type SummaryTokenKind uint8

const (
	// SummaryTokenWorkspace identifies a keyed workspace value. The remaining
	// constants identify other fixed metadata dimensions.
	SummaryTokenWorkspace SummaryTokenKind = iota + 1
	// SummaryTokenSession identifies a keyed session value.
	SummaryTokenSession
	// SummaryTokenClient identifies a keyed client value.
	SummaryTokenClient
	// SummaryTokenAgent identifies a keyed agent value.
	SummaryTokenAgent
	// SummaryTokenEventKind identifies a keyed event-kind value.
	SummaryTokenEventKind
)

// SegmentSummaryToken is an exact HMAC-SHA256 candidate token.
type SegmentSummaryToken struct {
	Kind  SummaryTokenKind
	Value [sha256.Size]byte
}

// SegmentBloomV1 is a versioned Bloom filter supplied by the canonical
// metadata summarizer. Bits are opaque here; generation belongs to #1650.
type SegmentBloomV1 struct {
	Kind      SummaryTokenKind
	BitCount  uint32
	HashCount uint8
	Bits      []byte
}

// SegmentSessionAggregateV1 contains only a keyed session identity and counts.
type SegmentSessionAggregateV1 struct {
	SessionToken [sha256.Size]byte
	UnitCount    uint64
	AuditCount   uint64
}

// SegmentCatalogSummaryV1 is stored inside the immutable segment, never in a
// sidecar. FilterKeyID is a non-secret key identifier.
type SegmentCatalogSummaryV1 struct {
	FilterKeyID  string
	ExactTokens  []SegmentSummaryToken
	Blooms       []SegmentBloomV1
	Sessions     []SegmentSessionAggregateV1
	TimeComplete bool
}

// CanonicalBytes validates, sorts, and encodes the fixed summary descriptor.
func (s SegmentCatalogSummaryV1) CanonicalBytes(maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || s.FilterKeyID == "" {
		return nil, fmt.Errorf("invalid segment summary descriptor")
	}
	// Preflight every caller-controlled count and byte length before copying,
	// sorting, or growing the canonical buffer.
	total := uint64(8)
	add := func(n uint64) error {
		if n > uint64(maxBytes) || total > uint64(maxBytes)-n {
			return fmt.Errorf("segment summary exceeds byte cap")
		}
		total += n
		return nil
	}
	if err := add(uint64(uvarintSize(uint64(len(s.FilterKeyID)))) + uint64(len(s.FilterKeyID))); err != nil {
		return nil, err
	}
	if err := add(1 + uint64(uvarintSize(uint64(len(s.ExactTokens))))); err != nil {
		return nil, err
	}
	if uint64(len(s.ExactTokens)) > uint64(maxBytes)/33 {
		return nil, fmt.Errorf("segment summary exceeds byte cap")
	}
	if err := add(uint64(len(s.ExactTokens)) * 33); err != nil {
		return nil, err
	}
	if err := add(uint64(uvarintSize(uint64(len(s.Blooms))))); err != nil {
		return nil, err
	}
	for _, bloom := range s.Blooms {
		if err := add(6 + uint64(uvarintSize(uint64(len(bloom.Bits)))) + uint64(len(bloom.Bits))); err != nil {
			return nil, err
		}
	}
	if err := add(uint64(uvarintSize(uint64(len(s.Sessions))))); err != nil {
		return nil, err
	}
	for _, session := range s.Sessions {
		if err := add(32 + uint64(uvarintSize(session.UnitCount)) + uint64(uvarintSize(session.AuditCount))); err != nil {
			return nil, err
		}
	}
	tokens := append([]SegmentSummaryToken(nil), s.ExactTokens...)
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].Kind != tokens[j].Kind {
			return tokens[i].Kind < tokens[j].Kind
		}
		return bytes.Compare(tokens[i].Value[:], tokens[j].Value[:]) < 0
	})
	blooms := append([]SegmentBloomV1(nil), s.Blooms...)
	sort.Slice(blooms, func(i, j int) bool { return blooms[i].Kind < blooms[j].Kind })
	sessions := append([]SegmentSessionAggregateV1(nil), s.Sessions...)
	sort.Slice(sessions, func(i, j int) bool {
		return bytes.Compare(sessions[i].SessionToken[:], sessions[j].SessionToken[:]) < 0
	})
	b := append([]byte("TRSS"), 0, 0, 0, byte(SegmentSummaryV1))
	b = binary.AppendUvarint(b, uint64(len(s.FilterKeyID)))
	b = append(b, s.FilterKeyID...)
	if s.TimeComplete {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	b = binary.AppendUvarint(b, uint64(len(tokens)))
	var previousToken []byte
	for _, token := range tokens {
		if token.Kind < SummaryTokenWorkspace || token.Kind > SummaryTokenEventKind {
			return nil, fmt.Errorf("unknown summary token kind")
		}
		key := append([]byte{byte(token.Kind)}, token.Value[:]...)
		if bytes.Equal(key, previousToken) {
			return nil, fmt.Errorf("duplicate summary token")
		}
		previousToken = append(previousToken[:0], key...)
		b = append(b, byte(token.Kind))
		b = append(b, token.Value[:]...)
	}
	b = binary.AppendUvarint(b, uint64(len(blooms)))
	var previousKind SummaryTokenKind
	for _, bloom := range blooms {
		if bloom.Kind < SummaryTokenWorkspace || bloom.Kind > SummaryTokenEventKind || bloom.Kind <= previousKind || bloom.BitCount == 0 || bloom.BitCount > SegmentSummaryBloomMaxBitsV1 || bloom.HashCount == 0 || bloom.HashCount > 16 || uint64(len(bloom.Bits))*8 != uint64(bloom.BitCount) {
			return nil, fmt.Errorf("invalid summary bloom descriptor")
		}
		previousKind = bloom.Kind
		b = append(b, byte(bloom.Kind))
		b = binary.BigEndian.AppendUint32(b, bloom.BitCount)
		b = append(b, bloom.HashCount)
		b = binary.AppendUvarint(b, uint64(len(bloom.Bits)))
		b = append(b, bloom.Bits...)
	}
	b = binary.AppendUvarint(b, uint64(len(sessions)))
	var previousSession []byte
	for _, session := range sessions {
		if session.UnitCount == 0 || (previousSession != nil && bytes.Compare(previousSession, session.SessionToken[:]) >= 0) {
			return nil, fmt.Errorf("invalid session aggregate")
		}
		previousSession = append(previousSession[:0], session.SessionToken[:]...)
		b = append(b, session.SessionToken[:]...)
		b = binary.AppendUvarint(b, session.UnitCount)
		b = binary.AppendUvarint(b, session.AuditCount)
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("segment summary exceeds byte cap")
	}
	return b, nil
}

// TimeFilterMayMatch prevents a negative time decision when legacy timestamps
// were malformed and the segment's time summary is therefore incomplete.
func (s SegmentCatalogSummaryV1) TimeFilterMayMatch(segmentMin, segmentMax, queryStart, queryEnd time.Time) bool {
	if !s.TimeComplete || segmentMin.IsZero() || segmentMax.IsZero() || segmentMax.Before(segmentMin) || queryStart.IsZero() || queryEnd.IsZero() || queryEnd.Before(queryStart) {
		return true
	}
	return !segmentMax.Before(queryStart) && !segmentMin.After(queryEnd)
}
