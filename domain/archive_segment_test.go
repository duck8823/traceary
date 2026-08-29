package domain_test

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain"
)

func TestArchive_DoesNotCarryOutputMetadata(t *testing.T) {
	t.Parallel()

	columns := domain.ArchiveAuditV1Columns()
	if diff := cmp.Diff(27, len(columns)); diff != "" {
		t.Fatalf("ArchiveAuditV1Columns length mismatch (-want +got):\n%s", diff)
	}
	for _, column := range columns {
		if column == "output_metadata" {
			t.Fatal("v1 archive columns must stay frozen without output_metadata")
		}
	}
}

func TestHistoryUnitCanonicalBytesPreserveStorageClassesAndBytes(t *testing.T) {
	eventValues := make([]domain.SQLiteValue, 23)
	for i := range eventValues {
		eventValues[i] = domain.NullValue()
	}
	eventValues[0] = domain.TextValue([]byte("event-7"))
	eventValues[5] = domain.TextValue([]byte(time.Unix(1, 2).UTC().Format(time.RFC3339Nano)))
	eventValues[4] = domain.TextValue([]byte{0, 0xff})
	eventValues[10] = domain.IntegerValue(-2)
	eventValues[18] = domain.BlobValue([]byte{0, 0xff})
	event, err := domain.NewArchiveEventV1(eventValues)
	if err != nil {
		t.Fatal(err)
	}
	auditValues := make([]domain.SQLiteValue, 27)
	for i := range auditValues {
		auditValues[i] = domain.NullValue()
	}
	auditValues[0] = domain.TextValue([]byte("cmd"))
	auditValues[1] = domain.BlobValue([]byte{0xff})
	auditValues[5] = domain.IntegerValue(-1)
	auditValues[6] = domain.RealValue(1.25)
	audit, err := domain.NewArchiveAuditV1(auditValues)
	if err != nil {
		t.Fatal(err)
	}
	u := domain.HistoryUnit{Sequence: 7, Event: event, Audit: &audit}
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
	for i, value := range decoded.Event.Values() {
		want := eventValues[i]
		if value.Class != want.Class || value.Int != want.Int || value.Real != want.Real || !bytes.Equal(value.Bytes, want.Bytes) {
			t.Fatalf("event column %d did not round trip", i)
		}
	}
	if decoded.Audit == nil {
		t.Fatal("audit missing after round trip")
	}
	for i, value := range decoded.Audit.Values() {
		want := auditValues[i]
		if value.Class != want.Class || value.Int != want.Int || value.Real != want.Real || !bytes.Equal(value.Bytes, want.Bytes) {
			t.Fatalf("audit column %d did not round trip", i)
		}
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
	textValues := append([]domain.SQLiteValue(nil), eventValues...)
	textValues[4] = domain.TextValue([]byte("x"))
	textEvent, _ := domain.NewArchiveEventV1(textValues)
	blobValues := append([]domain.SQLiteValue(nil), eventValues...)
	blobValues[4] = domain.BlobValue([]byte("x"))
	blobEvent, _ := domain.NewArchiveEventV1(blobValues)
	text := domain.HistoryUnit{Sequence: 7, Event: textEvent}
	blob := domain.HistoryUnit{Sequence: 7, Event: blobEvent}
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
