package domain

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestSegmentSummaryCanonicalEncodingIsDeterministicAndRejectsDuplicates(t *testing.T) {
	a := SegmentSummaryToken{Kind: SummaryTokenSession, Value: sha256.Sum256([]byte("a"))}
	b := SegmentSummaryToken{Kind: SummaryTokenWorkspace, Value: sha256.Sum256([]byte("b"))}
	s := SegmentCatalogSummaryV1{FilterKeyID: "key-v1", TimeComplete: true, ExactTokens: []SegmentSummaryToken{a, b}}
	first, err := s.CanonicalBytes(4096)
	if err != nil {
		t.Fatal(err)
	}
	s.ExactTokens = []SegmentSummaryToken{b, a}
	second, err := s.CanonicalBytes(4096)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("canonical summary depends on caller order")
	}
	s.ExactTokens = []SegmentSummaryToken{a, a}
	if _, err = s.CanonicalBytes(4096); err == nil {
		t.Fatal("duplicate exact token accepted")
	}
}

func TestIncompleteTimeSummaryCannotProduceNegativeCandidate(t *testing.T) {
	s := SegmentCatalogSummaryV1{TimeComplete: false}
	if !s.TimeFilterMayMatch(time.Unix(1, 0), time.Unix(2, 0), time.Unix(100, 0), time.Unix(200, 0)) {
		t.Fatal("incomplete time summary excluded a segment")
	}
	s.TimeComplete = true
	if s.TimeFilterMayMatch(time.Unix(1, 0), time.Unix(2, 0), time.Unix(100, 0), time.Unix(200, 0)) {
		t.Fatal("disjoint complete bounds matched")
	}
}

func TestArchiveEventAllowsMalformedLegacyCreatedAt(t *testing.T) {
	values := make([]SQLiteValue, len(ArchiveEventV1Columns()))
	for i := range values {
		values[i] = NullValue()
	}
	values[0] = TextValue([]byte("legacy"))
	values[5] = TextValue([]byte{0xff, 'x'})
	event, err := NewArchiveEventV1(values)
	if err != nil {
		t.Fatal(err)
	}
	unit := HistoryUnit{Sequence: 1, Event: event}
	if !unit.CreatedAt().IsZero() {
		t.Fatal("malformed time became valid")
	}
	if _, err = unit.CanonicalBytes(); err != nil {
		t.Fatalf("raw legacy timestamp not preserved: %v", err)
	}
}

func TestSegmentSummaryPreflightUsesExactCanonicalSize(t *testing.T) {
	s := SegmentCatalogSummaryV1{FilterKeyID: string(make([]byte, 128)), TimeComplete: true,
		Blooms:   []SegmentBloomV1{{Kind: SummaryTokenWorkspace, BitCount: 1024, HashCount: 3, Bits: make([]byte, 128)}},
		Sessions: []SegmentSessionAggregateV1{{SessionToken: sha256.Sum256([]byte("s")), UnitCount: 128, AuditCount: 128}},
	}
	encoded, err := s.CanonicalBytes(4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CanonicalBytes(int64(len(encoded))); err != nil {
		t.Fatalf("exact encoded cap rejected: %v", err)
	}
	if _, err = s.CanonicalBytes(int64(len(encoded) - 1)); err == nil {
		t.Fatal("one-byte-short cap accepted")
	}
}
