package domain_test

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"github.com/duck8823/traceary/domain"
)

func TestHistoryUnitCanonicalBytesPreserveStorageClassesAndBytes(t *testing.T) {
	u := domain.HistoryUnit{Sequence: 7, Event: []domain.SQLiteValue{
		domain.NullValue(), domain.IntegerValue(-2), domain.RealValue(-0),
		domain.TextValue([]byte{0, 0xff}), domain.BlobValue([]byte{0, 0xff}),
	}, Audit: []domain.SQLiteValue{domain.TextValue(nil)}}
	a, err := u.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := u.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("canonical encoding is not deterministic")
	}
	decoded, err := domain.DecodeHistoryUnitCanonical(a)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := decoded.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, roundTrip) {
		t.Fatal("canonical round trip changed bytes or storage classes")
	}

	withoutAudit := u
	withoutAudit.Audit = nil
	c, err := withoutAudit.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, c) {
		t.Fatal("missing audit collapsed into present empty values")
	}
	text := domain.HistoryUnit{Sequence: 7, Event: []domain.SQLiteValue{domain.TextValue([]byte("x"))}}
	blob := domain.HistoryUnit{Sequence: 7, Event: []domain.SQLiteValue{domain.BlobValue([]byte("x"))}}
	tb, _ := text.CanonicalBytes()
	bb, _ := blob.CanonicalBytes()
	if bytes.Equal(tb, bb) {
		t.Fatal("text and blob storage classes collapsed")
	}
}

func TestSegmentIdentityIsDeterministicAndRangeBound(t *testing.T) {
	d := sha256.Sum256([]byte("logical"))
	a, err := domain.NewSegmentIdentity("store", 1, 2, d)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := domain.NewSegmentIdentity("store", 1, 3, d)
	if a.Basename() == b.Basename() {
		t.Fatal("range is not identity-bound")
	}
	if got := a.Basename(); len(got) != len("segment-v1-")+64+len(".sqlite") {
		t.Fatalf("unexpected basename %q", got)
	}
}
