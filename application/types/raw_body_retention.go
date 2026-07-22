package types

import "time"

// RawBodyCandidate identifies one exact persisted event-body version.
type RawBodyCandidate struct {
	EventID     string
	CreatedAt   time.Time
	StoredBytes int
	BodySHA256  string
}

// RawBodyRetentionSnapshot is the body-safe result of a read-only planner scan.
type RawBodyRetentionSnapshot struct {
	DatabaseIdentity  string
	SQLiteUserVersion int
	SnapshotAt        time.Time
	Candidates        []RawBodyCandidate
	ExcludedActive    []string
}

// RawBodyRecoveryBody contains one verified body used only at the executor boundary.
type RawBodyRecoveryBody struct {
	Candidate RawBodyCandidate
	Body      string
}

// RawBodyApplyResult reports an idempotent plan application.
type RawBodyApplyResult struct {
	PlanID         string
	CandidateCount int
	PrunedCount    int
	AlreadyPruned  int
}

// RawBodyRestoreResult reports an idempotent recovery operation.
type RawBodyRestoreResult struct {
	PlanID          string
	CandidateCount  int
	RestoredCount   int
	AlreadyRestored int
}
