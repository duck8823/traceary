package domain

import (
	"fmt"
	"strings"
)

// UnavailableRetentionApproval is the immutable evidence that an operator
// approved dropping a specific unavailable_retention marker set. Absence of
// this value on PreparedStoreUpgradeRun is the unapproved state.
//
// Plan binds run id and source identity after those values exist. Matches
// re-checks the bound run against a recomputed source identity, schema
// version, count and sorted-id-set digest immediately before Build.
type UnavailableRetentionApproval struct {
	RunID          string            `json:"run_id"`
	SourceIdentity StoreFileIdentity `json:"source_identity"`
	SchemaVersion  int64             `json:"schema_version"`
	RowCount       int64             `json:"row_count"`
	SetDigest      string            `json:"set_digest"`
}

// NewUnavailableRetentionApproval validates the operator-facing set identity
// (count, digest, schema version). Run id and source identity are bound later.
func NewUnavailableRetentionApproval(rowCount int64, setDigest string, schemaVersion int64) (UnavailableRetentionApproval, error) {
	digest := strings.TrimSpace(setDigest)
	if rowCount < 0 {
		return UnavailableRetentionApproval{}, fmt.Errorf("unavailable retention approval row count must not be negative")
	}
	if digest == "" {
		return UnavailableRetentionApproval{}, fmt.Errorf("unavailable retention approval set digest must not be empty")
	}
	if schemaVersion < 0 {
		return UnavailableRetentionApproval{}, fmt.Errorf("unavailable retention approval schema version must not be negative")
	}
	return UnavailableRetentionApproval{RowCount: rowCount, SetDigest: digest, SchemaVersion: schemaVersion}, nil
}

// Bind stamps the durable run identity onto a copy of the approval.
func (a UnavailableRetentionApproval) Bind(runID string, sourceIdentity StoreFileIdentity) UnavailableRetentionApproval {
	a.RunID = strings.TrimSpace(runID)
	a.SourceIdentity = sourceIdentity
	return a
}

// Matches reports whether the approval still describes the inspected run.
func (a UnavailableRetentionApproval) Matches(runID string, sourceIdentity StoreFileIdentity, schemaVersion int64, rowCount int64, setDigest string) bool {
	return a.RunID == runID &&
		a.SourceIdentity == sourceIdentity &&
		a.SchemaVersion == schemaVersion &&
		a.RowCount == rowCount &&
		a.SetDigest == setDigest
}
