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
	Selected, Written, Evicted, Cleaned     int
	StoredBytes, DecodedBytes, WrittenBytes int64
	CleanupBytes                            int64
	Completed                               bool
	GenerationID                            string
}

// ProjectionDocument is canonical, hydrated input. It deliberately contains no
// SQLite identity or DTO; Sequence is the stable application checkpoint.
type ProjectionDocument struct {
	Sequence                                             int64
	EventID, SessionID, CreatedAt, Text, PreviousSummary string
	StoredBytes, DecodedBytes                            int64
	Deleted                                              bool
	CommandCount, FailureCount                           int
}

type ProjectionSnapshot struct {
	Generation    SearchProjectionGeneration
	Phase         string
	Documents     []ProjectionDocument
	SourceDone    bool
	RetainedBytes int64
	Cleanup       []ProjectionCleanupCandidate
	CleanupDone   bool
	Now           time.Time
}

type ProjectionCleanupCandidate struct {
	Class               string
	RowID, LogicalBytes int64
}

type ProjectionWrite struct {
	Document     ProjectionDocument
	Summary      string
	Keywords     map[string]int
	LogicalBytes int64
	RetainRecent bool
}

// ProjectionBatchPlan is a pure application-owned decision. Adapters may only
// persist this decision; they must not extend it with additional rows.
type ProjectionBatchPlan struct {
	GenerationID, Phase                                  string
	ExpectedRevision, ExpectedCheckpoint, NextCheckpoint int64
	Writes                                               []ProjectionWrite
	Cleanup                                              []ProjectionCleanupCandidate
	NextPhase                                            string
	Completed                                            bool
	Ledger                                               BudgetLedger
}

// BudgetLedger accounts every bounded resource class, including conservative
// SQLite WAL amplification for writes and deletes.
type BudgetLedger struct {
	Rows                                                              int
	StoredBytes, DecodedBytes, LogicalWriteBytes, WALReservationBytes int64
}

// ReserveSource is the application-owned hard-cap rule used before hydration.
func (l *BudgetLedger) ReserveSource(b SearchProjectionBudget, stored, decoded int64) bool {
	if l.Rows >= b.Rows || l.StoredBytes+stored > b.StoredBytes || l.DecodedBytes+decoded > b.DecodedBytes {
		return false
	}
	l.Rows++
	l.StoredBytes += stored
	l.DecodedBytes += decoded
	return true
}

// AdmitSource distinguishes an impossible first row from a resumable prefix.
func (l *BudgetLedger) AdmitSource(b SearchProjectionBudget, stored, decoded int64) (bool, error) {
	if stored > b.StoredBytes {
		return false, &SearchProjectionOversizeError{Class: "stored_bytes", Bytes: stored, Limit: b.StoredBytes}
	}
	if decoded > b.DecodedBytes {
		return false, &SearchProjectionOversizeError{Class: "decoded_bytes", Bytes: decoded, Limit: b.DecodedBytes}
	}
	return l.ReserveSource(b, stored, decoded), nil
}

// RetentionPlan is the pure eviction/old-generation cleanup decision.
type ProjectionRetentionPlan struct {
	Candidates []ProjectionCleanupCandidate
	Ledger     BudgetLedger
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
