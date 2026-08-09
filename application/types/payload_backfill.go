package types

// PayloadBackfillRecipeVersion is the batch semantics identifier stored on
// every run. Resume refuses a checkpoint whose version differs so a newer
// binary cannot skip a prefix written under different rules.
const PayloadBackfillRecipeVersion = "events-body-zstd-v1"

// PayloadBackfillState is the persisted live-store backfill workflow state.
type PayloadBackfillState string

const (
	// PayloadBackfillRunning owns an active in-place rewrite.
	PayloadBackfillRunning PayloadBackfillState = "running"
	// PayloadBackfillPaused is resumable at a committed batch boundary.
	PayloadBackfillPaused PayloadBackfillState = "paused"
	// PayloadBackfillCompleted means every eligible row at or below the
	// high-water was rewritten (or kept as identity after a shrink check).
	PayloadBackfillCompleted PayloadBackfillState = "completed"
	// PayloadBackfillFailed is a closed failure (partial metadata, etc.).
	PayloadBackfillFailed PayloadBackfillState = "failed"
)

// CanResume reports whether another worker may continue the run.
func (s PayloadBackfillState) CanResume() bool {
	return s == PayloadBackfillRunning || s == PayloadBackfillPaused
}

// PayloadBackfillConfig bounds one live-store rewrite invocation.
type PayloadBackfillConfig struct {
	BatchRows int `json:"batch_rows"`
	// StopAfterBatches pauses successfully after this many committed batches
	// within the invocation (0 means no limit). Used for operator pacing and
	// resume evidence; it is not part of the recipe version.
	StopAfterBatches int64 `json:"-"`
}

// MaxPayloadBackfillBatchRows bounds query and in-memory page cardinality.
const MaxPayloadBackfillBatchRows = 4096

// DefaultPayloadBackfillBatchRows is the operator default.
const DefaultPayloadBackfillBatchRows = 256

// Valid reports whether every bound is explicit and positive.
func (c PayloadBackfillConfig) Valid() bool {
	return c.BatchRows > 0 && c.BatchRows <= MaxPayloadBackfillBatchRows && c.StopAfterBatches >= 0
}

// PayloadBackfillResult is sanitized aggregate evidence for CLI/JSON output.
type PayloadBackfillResult struct {
	RunID               string `json:"run_id,omitempty"`
	State               string `json:"state"`
	RecipeVersion       string `json:"recipe_version,omitempty"`
	HighWaterRowID      int64  `json:"high_water_rowid,omitempty"`
	CursorRowID         int64  `json:"cursor_rowid,omitempty"`
	PassCount           int64  `json:"pass_count,omitempty"`
	EligibleRows        int64  `json:"eligible_rows,omitempty"`
	ScannedRows         int64  `json:"scanned_rows"`
	EncodedRows         int64  `json:"encoded_rows"`
	IdentityKeptRows    int64  `json:"identity_kept_rows"`
	ConflictedRows      int64  `json:"conflicted_rows"`
	PartialMetadataRows int64  `json:"partial_metadata_rows"`
	RewrittenRows       int64  `json:"rewritten_rows"`
	PlaintextBytes      int64  `json:"plaintext_bytes"`
	StoredBytes         int64  `json:"stored_bytes"`
	BatchCount          int64  `json:"batch_count,omitempty"`
	MorePending         bool   `json:"more_pending"`
	FailureEventID      string `json:"failure_event_id,omitempty"`
	FailureReason       string `json:"failure_reason,omitempty"`
	StartedAt           string `json:"started_at,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
	CompletedAt         string `json:"completed_at,omitempty"`
}
