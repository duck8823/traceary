package types

import "time"

// SearchProjectionBudget bounds one rebuild invocation and the retained recent
// corpus. All limits are mandatory, making accidental unbounded rebuilds
// impossible at the adapter boundary.
type SearchProjectionBudget struct {
	Rows         int
	WallTime     time.Duration
	LockTime     time.Duration
	StoredBytes  int64
	DecodedBytes int64
	WriteBytes   int64
	RecentAge    time.Duration
	RecentBytes  int64
}

// Valid reports whether every rebuild and retention dimension is bounded.
func (b SearchProjectionBudget) Valid() bool {
	return b.Rows > 0 && b.WallTime > 0 && b.LockTime > 0 && b.StoredBytes > 0 && b.DecodedBytes > 0 && b.WriteBytes > 0 && b.RecentAge > 0 && b.RecentBytes > 0
}

// SearchProjectionProgress is payload-free rebuild evidence.
type SearchProjectionProgress struct {
	Selected, Written, Evicted              int
	StoredBytes, DecodedBytes, WrittenBytes int64
	Completed                               bool
}

// SearchProjectionStatus is metadata-only projection provenance and capacity.
type SearchProjectionStatus struct {
	ProjectionVersion int    `json:"projection_version"`
	FTSDesign         string `json:"fts_design"`
	Completed         bool   `json:"completed"`
	RecentAgeSeconds  int64  `json:"recent_age_seconds"`
	RecentByteLimit   int64  `json:"recent_byte_limit"`
	RecentBytes       int64  `json:"recent_bytes"`
	RecentDocuments   int64  `json:"recent_documents"`
	SummarySessions   int64  `json:"summary_sessions"`
	KeywordRows       int64  `json:"keyword_rows"`
}
