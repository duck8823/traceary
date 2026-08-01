package types

import "time"

// PayloadRehearsalState is the persisted copied-store workflow state.
type PayloadRehearsalState string

// PayloadRehearsalRunMode makes start/resume intent explicit at the port boundary.
type PayloadRehearsalRunMode string

const (
	PayloadRehearsalStart  PayloadRehearsalRunMode = "start"
	PayloadRehearsalResume PayloadRehearsalRunMode = "resume"
)

// PayloadRehearsalRunCommand describes the lifecycle entry requested by the use case.
type PayloadRehearsalRunCommand struct{ Mode PayloadRehearsalRunMode }

// Valid reports whether the command names a supported lifecycle entry.
func (c PayloadRehearsalRunCommand) Valid() bool {
	return c.Mode == PayloadRehearsalStart || c.Mode == PayloadRehearsalResume
}

// IsResume reports whether an existing resumable run is required.
func (c PayloadRehearsalRunCommand) IsResume() bool { return c.Mode == PayloadRehearsalResume }

// PayloadRehearsalField identifies one payload lane without exposing SQLite
// table or column names to orchestration.
type PayloadRehearsalField string

const (
	PayloadRehearsalEventBody   PayloadRehearsalField = "event_body"
	PayloadRehearsalCommandText PayloadRehearsalField = "command_text"
	PayloadRehearsalInputText   PayloadRehearsalField = "input_text"
	PayloadRehearsalOutputText  PayloadRehearsalField = "output_text"
)

// OrderedPayloadRehearsalFields is the application-owned workflow order.
func OrderedPayloadRehearsalFields() []PayloadRehearsalField {
	return []PayloadRehearsalField{PayloadRehearsalEventBody, PayloadRehearsalCommandText, PayloadRehearsalInputText, PayloadRehearsalOutputText}
}

const (
	// PayloadRehearsalRunning owns an active writer lease.
	PayloadRehearsalRunning PayloadRehearsalState = "running"
	// PayloadRehearsalPaused is resumable and keeps canonical rows frozen.
	PayloadRehearsalPaused PayloadRehearsalState = "paused"
	// PayloadRehearsalCompleted awaits a verified scrub.
	PayloadRehearsalCompleted PayloadRehearsalState = "completed"
	// PayloadRehearsalScrubbed is verified and releases the source freeze.
	PayloadRehearsalScrubbed PayloadRehearsalState = "scrubbed"
	// PayloadRehearsalRolledBack records physical recovery completion.
	PayloadRehearsalRolledBack PayloadRehearsalState = "rolled_back"
	// PayloadRehearsalFailed is a recoverable terminal processing failure.
	PayloadRehearsalFailed PayloadRehearsalState = "failed"
)

// FreezesCanonical reports whether source rows must remain immutable.
func (s PayloadRehearsalState) FreezesCanonical() bool {
	return s == PayloadRehearsalRunning || s == PayloadRehearsalPaused || s == PayloadRehearsalCompleted
}

// CanResume reports whether another leased worker may resume the run.
func (s PayloadRehearsalState) CanResume() bool {
	return s == PayloadRehearsalRunning || s == PayloadRehearsalPaused
}

// CanScrub reports whether shadow rows are complete and eligible for verification.
func (s PayloadRehearsalState) CanScrub() bool {
	return s == PayloadRehearsalCompleted || s == PayloadRehearsalScrubbed
}

// PayloadRehearsalConfig contains mandatory bounds for a copied-store rehearsal.
type PayloadRehearsalConfig struct {
	TargetPath       string        `json:"-"`
	LivePath         string        `json:"-"`
	BackupPath       string        `json:"-"`
	BatchRows        int           `json:"batch_rows"`
	StoredByteLimit  int64         `json:"stored_byte_limit"`
	DecodedByteLimit int64         `json:"decoded_byte_limit"`
	WallTimeLimit    time.Duration `json:"wall_time_limit"`
	LockTimeLimit    time.Duration `json:"lock_time_limit"`
	ScrubByteLimit   int64         `json:"scrub_byte_limit"`
	ScrubTimeLimit   time.Duration `json:"scrub_time_limit"`
	MaxWALBytes      int64         `json:"max_wal_bytes"`
}

// Valid reports whether every safety/resource bound is explicit and positive.
func (c PayloadRehearsalConfig) Valid() bool {
	return c.TargetPath != "" && c.LivePath != "" && c.BatchRows > 0 && c.StoredByteLimit > 0 &&
		c.DecodedByteLimit > 0 && c.WallTimeLimit > 0 && c.LockTimeLimit > 0 &&
		c.ScrubByteLimit > 0 && c.ScrubTimeLimit > 0 && c.MaxWALBytes > 0
}

// ReadinessGateStatus is an evidence-backed prerequisite outcome.
type ReadinessGateStatus string

const (
	// ReadinessUnknown means the prerequisite has not been evidenced.
	ReadinessUnknown ReadinessGateStatus = "unknown"
	// ReadinessPassed means current evidence proves the prerequisite.
	ReadinessPassed ReadinessGateStatus = "passed"
	// ReadinessFailed means current evidence disproves the prerequisite.
	ReadinessFailed ReadinessGateStatus = "failed"
)

// PayloadActivationReadiness reports v0.35 prerequisites without activating them.
type PayloadActivationReadiness struct {
	CompatibleReader          bool                `json:"compatible_reader"`
	LiveIdentityOnly          bool                `json:"live_identity_only"`
	BackupVerified            bool                `json:"backup_verified"`
	HeadroomSufficient        bool                `json:"headroom_sufficient"`
	RehearsalComplete         bool                `json:"rehearsal_complete"`
	ScrubPassed               bool                `json:"scrub_passed"`
	RollbackVerified          bool                `json:"rollback_verified"`
	ActivationAllowed         bool                `json:"activation_allowed"`
	MinimumReaderStatus       ReadinessGateStatus `json:"minimum_reader_status"`
	OldProcessesStoppedStatus ReadinessGateStatus `json:"old_processes_stopped_status"`
	BackupStatus              ReadinessGateStatus `json:"backup_status"`
	HeadroomStatus            ReadinessGateStatus `json:"headroom_status"`
	ScrubStatus               ReadinessGateStatus `json:"scrub_status"`
	RollbackStatus            ReadinessGateStatus `json:"rollback_status"`
	EvidenceAt                time.Time           `json:"evidence_at"`
}

// PayloadRehearsalFileState is a body-free DB/WAL/SHM snapshot.
type PayloadRehearsalFileState struct {
	Component string `json:"component"`
	Exists    bool   `json:"exists"`
	SizeBytes int64  `json:"size_bytes"`
	ModUnixNS int64  `json:"mtime_unix_ns"`
	Identity  string `json:"identity"`
}

// PayloadRehearsalMetrics contains sanitized aggregate rehearsal evidence.
type PayloadRehearsalMetrics struct {
	RunID                  string                      `json:"run_id,omitempty"`
	State                  string                      `json:"state"`
	ScannedRows            int64                       `json:"scanned_rows"`
	EncodedRows            int64                       `json:"encoded_rows"`
	PlaintextBytes         int64                       `json:"plaintext_bytes"`
	StoredBytes            int64                       `json:"stored_bytes"`
	BatchCount             int64                       `json:"batch_count"`
	BatchDurationHistogram map[string]int64            `json:"batch_duration_histogram"`
	ConflictRows           int64                       `json:"conflict_rows"`
	PeakWALBytes           int64                       `json:"peak_wal_bytes"`
	FreeBytes              uint64                      `json:"free_bytes"`
	EstimatedHeadroom      uint64                      `json:"estimated_headroom_bytes"`
	DryRunZeroWrite        bool                        `json:"dry_run_zero_write"`
	MigrationRequired      bool                        `json:"migration_required"`
	LiveIdentityOnly       bool                        `json:"live_identity_only"`
	RollbackDigest         string                      `json:"rollback_digest,omitempty"`
	RollbackVerified       bool                        `json:"rollback_verified"`
	Before                 []PayloadRehearsalFileState `json:"before,omitempty"`
	After                  []PayloadRehearsalFileState `json:"after,omitempty"`
	ActivationReadiness    PayloadActivationReadiness  `json:"activation_readiness"`
}
