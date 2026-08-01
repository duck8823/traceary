//nolint:revive // Public projection API names are intentionally explicit.
package types

import (
	"fmt"
	"time"
)

type SearchProjectionBudget struct {
	Rows                                  int
	WallTime, LockTime                    time.Duration
	StoredBytes, DecodedBytes, WriteBytes int64
	RecentAge                             time.Duration
	RecentBytes                           int64
}

func (b SearchProjectionBudget) Valid() bool {
	return b.Rows > 0 && b.WallTime > 0 && b.LockTime > 0 && b.StoredBytes > 0 && b.DecodedBytes > 0 && b.WriteBytes > 0 && b.RecentAge > 0 && b.RecentBytes > 0
}
func (b SearchProjectionBudget) ConfigHash() string {
	return fmt.Sprintf("v1:%d:%d:%d:%d:%d:%d:%d:%d", b.Rows, b.WallTime.Nanoseconds(), b.LockTime.Nanoseconds(), b.StoredBytes, b.DecodedBytes, b.WriteBytes, b.RecentAge.Nanoseconds(), b.RecentBytes)
}

type SearchProjectionGeneration struct {
	GenerationID                          string
	ConfigHash                            string
	SourceRevision, HighWater, Checkpoint int64
}
type SearchProjectionProgress struct {
	Selected, Written, Evicted              int
	StoredBytes, DecodedBytes, WrittenBytes int64
	Completed                               bool
	GenerationID                            string
}

type SearchProjectionOversizeError struct {
	Class        string
	Bytes, Limit int64
}

func (e *SearchProjectionOversizeError) Error() string {
	return fmt.Sprintf("search projection %s exceeds batch budget (%d > %d)", e.Class, e.Bytes, e.Limit)
}

type SearchProjectionNoProgressError struct{ Reason string }

func (e *SearchProjectionNoProgressError) Error() string {
	return "search projection made no progress: " + e.Reason
}

type SearchProjectionDriftError struct{}

func (*SearchProjectionDriftError) Error() string {
	return "search projection source changed; start a new generation"
}

type SearchProjectionStatus struct {
	SchemaVersion          string           `json:"schema_version"`
	State                  string           `json:"state"`
	ProjectionVersion      int              `json:"projection_version"`
	FTSDesign              string           `json:"fts_design"`
	ConfigHash             string           `json:"config_hash"`
	SourceRevision         int64            `json:"source_revision"`
	HighWater              int64            `json:"high_water"`
	Checkpoint             int64            `json:"checkpoint"`
	Completed              bool             `json:"completed"`
	RecentAgeSeconds       int64            `json:"recent_age_seconds"`
	RecentByteLimit        int64            `json:"recent_byte_limit"`
	RecentBytes            int64            `json:"recent_bytes"`
	RecentDocuments        int64            `json:"recent_documents"`
	SummarySessions        int64            `json:"summary_sessions"`
	KeywordRows            int64            `json:"keyword_rows"`
	SummaryLogicalBytes    int64            `json:"summary_logical_bytes"`
	KeywordLogicalBytes    int64            `json:"keyword_logical_bytes"`
	FTSLogicalBytes        int64            `json:"fts_logical_bytes"`
	PhysicalBytes          int64            `json:"physical_bytes"`
	PhysicalEvidence       CapacityEvidence `json:"physical_evidence"`
	LastBatchMilliseconds  int64            `json:"last_batch_milliseconds"`
	InspectionMilliseconds int64            `json:"inspection_milliseconds"`
	KeywordVersion         int              `json:"keyword_version"`
}
