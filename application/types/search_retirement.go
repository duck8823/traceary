//nolint:revive // Names are explicit protocol-neutral search maintenance contracts.
package types

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/duck8823/traceary/domain/model"
)

const SearchParityV2Schema = "traceary.tiered-search-parity/v2"

type SearchParityRevision struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}
type SearchParityProjection struct {
	Revision      int64 `json:"revision"`
	HighWater     int64 `json:"high_water"`
	LogicalBytes  int64 `json:"logical_bytes"`
	PhysicalBytes int64 `json:"physical_bytes"`
}
type SearchParityCriterion struct {
	QueryClass       string `json:"query_class"`
	CriterionBinding string `json:"criterion_binding"`
	Status           string `json:"status"`
	ComparisonEqual  bool   `json:"comparison_equal"`
	CoverageComplete bool   `json:"coverage_complete"`
	LegacyLatencyUS  int64  `json:"legacy_latency_us"`
	TieredLatencyUS  int64  `json:"tiered_latency_us"`
}
type SearchParityV2Evidence struct {
	SchemaVersion      string                  `json:"schema_version"`
	AuthorizationScope string                  `json:"authorization_scope"`
	TargetStoreBinding string                  `json:"target_store_binding"`
	Revision           SearchParityRevision    `json:"revision"`
	Projection         SearchParityProjection  `json:"projection"`
	Criteria           []SearchParityCriterion `json:"criteria"`
}

// SearchRetirementSnapshot is a fresh, same-connection authorization input.
// CursorKey is secret material and must never be rendered or persisted again.
type SearchRetirementSnapshot struct {
	State                                                        model.SearchMaintenance
	CursorKey                                                    []byte
	ProjectionGeneration                                         string
	ProjectionRevision, ProjectionHighWater, SourceHighWater     int64
	ProjectionState                                              string
	EventCount, AuditCount, CanonicalLogicalBytes, PhysicalBytes int64
	TargetAdopted                                                bool
}

type SearchMaintenanceReport struct {
	Authority           model.SearchAuthority        `json:"authority"`
	Phase               model.SearchMaintenancePhase `json:"phase"`
	Progress            int64                        `json:"progress"`
	LogicalBytesBefore  int64                        `json:"logical_bytes_before"`
	LogicalBytesAfter   int64                        `json:"logical_bytes_after"`
	PhysicalBytesBefore int64                        `json:"physical_bytes_before"`
	PhysicalBytesAfter  int64                        `json:"physical_bytes_after"`
	RowsProcessed       int64                        `json:"rows_processed"`
	Complete            bool                         `json:"complete"`
}

func KeyedSearchParityBinding(key []byte, purpose string, fields ...string) (string, error) {
	if len(key) < 16 || purpose == "" {
		return "", fmt.Errorf("binding unavailable")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, "traceary:search-parity:v2\x00"+purpose)
	for _, field := range fields {
		_, _ = io.WriteString(mac, fmt.Sprintf("\x00%d:", len(field)))
		_, _ = io.WriteString(mac, field)
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func SearchParityTargetFields(revision SearchParityRevision, projection SearchParityProjection, events, audits, canonicalBytes int64) []string {
	return []string{
		fmt.Sprintf("aggregate=events:%d,audits:%d,canonical_bytes:%d", events, audits, canonicalBytes),
		fmt.Sprintf("revision=%s:%t", revision.Commit, revision.Dirty),
		fmt.Sprintf("projection=%d:%d:%d:%d", projection.Revision, projection.HighWater, projection.LogicalBytes, projection.PhysicalBytes),
	}
}
