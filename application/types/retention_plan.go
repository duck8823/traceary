package types

// RetentionPlan is the reviewed JSON envelope consumed by apply/restore.
type RetentionPlan struct {
	PlanID           string                    `json:"plan_id"`
	CanonicalPayload RetentionCanonicalPayload `json:"canonical_payload"`
	Display          RetentionPlanDisplay      `json:"display,omitempty"`
}

// RetentionCanonicalPayload is the hashed portion of retention-plan/v1.
type RetentionCanonicalPayload struct {
	SchemaVersion        string                   `json:"schema_version"`
	CreatedAt            string                   `json:"created_at"`
	SnapshotAt           string                   `json:"snapshot_at"`
	Source               RetentionPlanSource      `json:"source"`
	Policy               RetentionPlanPolicy      `json:"policy"`
	ClassResults         []RetentionClassResult   `json:"class_results"`
	Candidates           []RetentionPlanCandidate `json:"candidates"`
	Exclusions           []RetentionPlanExclusion `json:"exclusions"`
	RecoveryRequirements []RetentionRecoveryPoint `json:"recovery_requirements"`
	Phases               []RetentionPlanPhase     `json:"phases"`
}

// RetentionPlanDisplay contains non-authoritative operator-facing fields.
type RetentionPlanDisplay struct {
	Summary string `json:"summary,omitempty"`
}

// RetentionPlanSource identifies the exact source store and reviewed roots.
type RetentionPlanSource struct {
	DatabaseIdentity  string              `json:"database_identity"`
	SQLiteUserVersion int                 `json:"sqlite_user_version"`
	MigrationDigest   string              `json:"migration_digest"`
	Roots             []RetentionPlanRoot `json:"roots"`
}

// RetentionPlanRoot identifies one reviewed filesystem root without exposing it.
type RetentionPlanRoot struct {
	RootID      string `json:"root_id"`
	Fingerprint string `json:"fingerprint"`
}

// RetentionPlanPolicy records the configured ceilings used by the planner.
type RetentionPlanPolicy struct {
	Ceilings []RetentionPolicyCeiling `json:"ceilings"`
}

// RetentionPolicyCeiling records one class-specific policy ceiling.
type RetentionPolicyCeiling struct {
	Class   string `json:"class"`
	Ceiling string `json:"ceiling"`
	Value   string `json:"value"`
}

// RetentionExtent distinguishes known byte values from unknown extents.
type RetentionExtent struct {
	Availability string `json:"availability"`
	Bytes        string `json:"bytes,omitempty"`
}

// RetentionCeilingResult reports current and projected status for one ceiling.
type RetentionCeilingResult struct {
	Ceiling   string          `json:"ceiling"`
	Status    string          `json:"status"`
	Current   RetentionExtent `json:"current"`
	Projected RetentionExtent `json:"projected"`
}

// RetentionClassResult reports the AND-reduced status of one retention class.
type RetentionClassResult struct {
	Class    string                   `json:"class"`
	Status   string                   `json:"status"`
	Ceilings []RetentionCeilingResult `json:"ceilings"`
}

// RetentionPlanCandidate is one exact reviewed database or file identity.
type RetentionPlanCandidate struct {
	Class             string          `json:"class"`
	IdentityKind      string          `json:"identity_kind"`
	DatabaseIdentity  string          `json:"database_identity"`
	RootID            string          `json:"root_id"`
	RelativePath      string          `json:"relative_path"`
	Timestamp         string          `json:"timestamp"`
	CandidateIdentity string          `json:"candidate_identity"`
	LogicalExtent     RetentionExtent `json:"logical_extent"`
	AllocatedExtent   RetentionExtent `json:"allocated_extent"`
	Reasons           []string        `json:"reasons"`
}

// RetentionPlanExclusion records one stable identity excluded from apply.
type RetentionPlanExclusion struct {
	Reason         string `json:"reason"`
	StableIdentity string `json:"stable_identity"`
}

// RetentionRecoveryPoint pins the verified recovery artifact required by apply.
type RetentionRecoveryPoint struct {
	Generation     string `json:"generation"`
	Digest         string `json:"digest"`
	RootID         string `json:"root_id"`
	RelativePath   string `json:"relative_path"`
	CoverageDigest string `json:"coverage_digest"`
	State          string `json:"state"`
}

// RetentionPlanBatch groups ordered candidate identities for one transaction.
type RetentionPlanBatch struct {
	Ordinal             string   `json:"ordinal"`
	CandidateIdentities []string `json:"candidate_identities"`
}

// RetentionPlanPhase records ordered execution steps for one retention phase.
type RetentionPlanPhase struct {
	Phase        string               `json:"phase"`
	Batches      []RetentionPlanBatch `json:"batches"`
	OrderedSteps []string             `json:"ordered_steps"`
}
