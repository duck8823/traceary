package application

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// RawBodyRetentionPlanner provides a body-free snapshot for immutable plan creation.
type RawBodyRetentionPlanner interface {
	ListRawBodyCandidates(ctx context.Context, before time.Time) (apptypes.RawBodyRetentionSnapshot, error)
	RetentionMarkerEncodedBytes() (int, error)
}

// RawBodyRetentionExecutor applies and restores exact reviewed body identities.
type RawBodyRetentionExecutor interface {
	// ApplyRawBodyPlan rechecks candidate eligibility against cutoffAt, the
	// plan's persisted selection boundary, not the wall clock at apply time.
	ApplyRawBodyPlan(ctx context.Context, databaseIdentity string, sqliteUserVersion int, migrationDigest, planID string, candidates []apptypes.RawBodyCandidate, cutoffAt, appliedAt time.Time) (apptypes.RawBodyApplyResult, error)
	RestoreRawBodyPlan(ctx context.Context, databaseIdentity string, sqliteUserVersion int, migrationDigest, planID string, bodies []apptypes.RawBodyRecoveryBody, restoredAt time.Time) (apptypes.RawBodyRestoreResult, error)
}
