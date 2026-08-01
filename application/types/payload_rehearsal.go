package types

import "time"

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
}

// Valid reports whether every safety/resource bound is explicit and positive.
func (c PayloadRehearsalConfig) Valid() bool {
	return c.TargetPath != "" && c.LivePath != "" && c.BatchRows > 0 && c.StoredByteLimit > 0 &&
		c.DecodedByteLimit > 0 && c.WallTimeLimit > 0 && c.LockTimeLimit > 0 &&
		c.ScrubByteLimit > 0 && c.ScrubTimeLimit > 0
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
	LiveIdentityOnly       bool                        `json:"live_identity_only"`
	RollbackDigest         string                      `json:"rollback_digest,omitempty"`
	RollbackVerified       bool                        `json:"rollback_verified"`
	Before                 []PayloadRehearsalFileState `json:"before,omitempty"`
	After                  []PayloadRehearsalFileState `json:"after,omitempty"`
}
