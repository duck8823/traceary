package types_test

import (
	"testing"

	"github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

func TestSegmentMigrationEvidenceV1DigestIsDeterministicAndRejectsPartial(t *testing.T) {
	zero := "0000000000000000000000000000000000000000000000000000000000000000"
	e := types.SegmentMigrationEvidenceV1{SchemaVersion: types.SegmentMigrationEvidenceSchemaV1, SoftwareCommit: "commit", ConfigDigest: zero, SourceDigest: zero, FormatVersion: 1, SummaryVersion: 1, SummaryDigest: zero, CompressionFloor: 32, PlanDigest: zero, Range: domain.CatalogRange{Start: 1, End: 1}, LineageDigest: zero, ManifestDigest: zero, FileDigest: zero, CatalogHeadBefore: types.CatalogHead{}, CatalogHeadAfter: types.CatalogHead{Epoch: 1}, CopiedRows: 1, CopiedPlainBytes: 10, SummaryRows: 1, SummaryBytes: 10, JournalRevision: 2, CatalogEpoch: 1}
	a, _, err := e.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := e.CanonicalDigest()
	if err != nil || a != b {
		t.Fatalf("digests = %q %q, %v", a, b, err)
	}
	e.SoftwareCommit = ""
	if _, _, err = e.CanonicalDigest(); err == nil {
		t.Fatal("partial evidence accepted")
	}
}
