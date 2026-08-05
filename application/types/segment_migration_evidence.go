package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/duck8823/traceary/domain"
)

// SegmentMigrationEvidenceSchemaV1 identifies the aggregate-only evidence schema.
const SegmentMigrationEvidenceSchemaV1 = "traceary.segment-migration-evidence/v1"

// ErrSegmentMigrationEvidenceInvalid rejects incomplete or unbound evidence.
var ErrSegmentMigrationEvidenceInvalid = errors.New("segment migration evidence is invalid")

// SegmentMigrationEvidenceV1 is aggregate-only release evidence. It contains
// no event, audit, search term, path, or filter-key material.
type SegmentMigrationEvidenceV1 struct {
	SchemaVersion     string              `json:"schema_version"`
	SoftwareCommit    string              `json:"software_commit"`
	ConfigDigest      string              `json:"config_digest"`
	SourceDigest      string              `json:"source_digest"`
	FormatVersion     uint32              `json:"format_version"`
	SummaryVersion    uint32              `json:"summary_version"`
	SummaryDigest     string              `json:"summary_digest"`
	CompressionFloor  int                 `json:"compression_floor"`
	PlanDigest        string              `json:"plan_digest"`
	Range             domain.CatalogRange `json:"range"`
	LineageDigest     string              `json:"lineage_digest"`
	ManifestDigest    string              `json:"manifest_digest"`
	FileDigest        string              `json:"file_digest"`
	CatalogHeadBefore CatalogHead         `json:"catalog_head_before"`
	CatalogHeadAfter  CatalogHead         `json:"catalog_head_after"`
	CopiedRows        int64               `json:"copied_rows"`
	CopiedPlainBytes  int64               `json:"copied_plain_bytes"`
	SummaryRows       uint64              `json:"summary_rows"`
	SummaryBytes      int64               `json:"summary_bytes"`
	JournalRevision   int64               `json:"journal_revision"`
	CatalogEpoch      int64               `json:"catalog_epoch"`
}

// CanonicalDigest returns the domain-separated digest and canonical JSON.
func (e SegmentMigrationEvidenceV1) CanonicalDigest() (string, []byte, error) {
	if e.SchemaVersion != SegmentMigrationEvidenceSchemaV1 || e.SoftwareCommit == "" || !domain.ValidCatalogDigest(e.ConfigDigest) || !domain.ValidCatalogDigest(e.SourceDigest) || !domain.ValidCatalogDigest(e.SummaryDigest) || !domain.ValidCatalogDigest(e.PlanDigest) || !domain.ValidCatalogDigest(e.LineageDigest) || !domain.ValidCatalogDigest(e.ManifestDigest) || !domain.ValidCatalogDigest(e.FileDigest) || e.Range.Start <= 0 || e.Range.End < e.Range.Start || e.FormatVersion == 0 || e.SummaryVersion == 0 || e.CopiedRows <= 0 || e.JournalRevision <= 0 || e.CatalogEpoch <= 0 {
		return "", nil, ErrSegmentMigrationEvidenceInvalid
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return "", nil, errors.Join(ErrSegmentMigrationEvidenceInvalid, err)
	}
	h := sha256.New()
	_, _ = h.Write([]byte("traceary/segment-migration-evidence/v1\x00"))
	_, _ = h.Write(encoded)
	return hex.EncodeToString(h.Sum(nil)), encoded, nil
}
