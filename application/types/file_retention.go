package types

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"time"
)

const (
	// BackupRetentionManifestSchema identifies the digest-bound sidecar contract.
	BackupRetentionManifestSchema = "traceary.backup.retention/v1"
	// BackupRetentionManifestPrefix reserves sidecars outside capacity inventory.
	BackupRetentionManifestPrefix = ".traceary-retention-backup-"
)

// BackupRetentionManifest binds a backup digest to its source-store lineage.
type BackupRetentionManifest struct {
	SchemaVersion string `json:"schema_version"`
	RelativePath  string `json:"relative_path"`
	BackupSHA256  string `json:"backup_sha256"`
	SourceLineage string `json:"source_lineage"`
	CreatedAt     string `json:"created_at"`
}

// BackupRetentionManifestName returns the reserved sidecar name for a root-relative backup.
func BackupRetentionManifestName(relativePath string) string {
	digest := sha256.Sum256([]byte("backup-retention-manifest/v1\x00" + filepath.Base(relativePath)))
	return BackupRetentionManifestPrefix + hex.EncodeToString(digest[:]) + ".json"
}

// FileRetentionBudgetInput configures optional class-specific ceilings.
type FileRetentionBudgetInput struct {
	MaxAge            *time.Duration
	MaxCount          *int
	MaxAllocatedBytes *int64
}

// FileRetentionPlanRequest contains the explicit roots and policies to inspect.
type FileRetentionPlanRequest struct {
	DatabasePath string
	OutputPath   string
	ExpiresAfter time.Duration
	Classes      []FileRetentionClassRequest
}

// FileRetentionClassRequest configures one independent local file class.
type FileRetentionClassRequest struct {
	Class  string
	Root   string
	Budget FileRetentionBudgetInput
}

// FileRetentionInventoryRequest asks the adapter for a read-only complete root snapshot.
type FileRetentionInventoryRequest struct {
	Class        string
	Root         string
	DatabasePath string
}

// FileRetentionInventorySnapshot is body-free verified filesystem evidence.
type FileRetentionInventorySnapshot struct {
	Class          string
	Root           string
	RootIdentity   string
	LiveGeneration string
	Entries        []FileRetentionInventoryEntry
}

// FileRetentionInventoryEntry is one exact regular file or fail-closed blocker.
type FileRetentionInventoryEntry struct {
	Identity             string
	RelativePath         string
	Device               uint64
	Inode                uint64
	LinkCount            uint64
	LogicalBytes         int64
	AllocatedBytes       int64
	AllocatedKnown       bool
	ModifiedAt           time.Time
	GenerationCreatedAt  time.Time
	GenerationProvenance string
	Generation           string
	ContentSHA256        string
	Verified             bool
	VerificationDigest   string
	VerificationReason   string
	MetadataRelativePath string
	MetadataSHA256       string
	Pinned               bool
	BlockingReason       string
}

// FileRetentionPlan is a strict immutable local cleanup plan.
type FileRetentionPlan struct {
	PlanID           string                        `json:"plan_id"`
	CanonicalPayload FileRetentionCanonicalPayload `json:"canonical_payload"`
	Display          FileRetentionPlanDisplay      `json:"display,omitempty"`
}

// FileRetentionCanonicalPayload is the hash-covered file-retention-plan/v1 contract.
type FileRetentionCanonicalPayload struct {
	SchemaVersion string                   `json:"schema_version"`
	CreatedAt     string                   `json:"created_at"`
	ExpiresAt     string                   `json:"expires_at"`
	DatabasePath  string                   `json:"database_path"`
	Classes       []FileRetentionClassPlan `json:"classes"`
}

// FileRetentionPlanDisplay contains non-authoritative operator text.
type FileRetentionPlanDisplay struct {
	Summary string `json:"summary,omitempty"`
}

// FileRetentionClassPlan contains one independent class decision.
type FileRetentionClassPlan struct {
	Class          string                       `json:"class"`
	Root           string                       `json:"root"`
	RootIdentity   string                       `json:"root_identity"`
	LiveGeneration string                       `json:"live_generation"`
	Budget         FileRetentionBudgetPlan      `json:"budget"`
	Inventory      []FileRetentionInventoryPlan `json:"inventory"`
	Status         string                       `json:"status"`
	Ceilings       []FileRetentionCeilingPlan   `json:"ceilings"`
	Floor          *FileRetentionFloorPlan      `json:"floor,omitempty"`
	Candidates     []FileRetentionCandidatePlan `json:"candidates"`
	Batches        []FileRetentionBatchPlan     `json:"batches"`
	OrderedSteps   []string                     `json:"ordered_steps"`
}

// FileRetentionBudgetPlan is a canonical string representation of optional ceilings.
type FileRetentionBudgetPlan struct {
	MaxAgeSeconds    string `json:"max_age_seconds,omitempty"`
	MaxCount         string `json:"max_count,omitempty"`
	MaxAllocatedByte string `json:"max_allocated_bytes,omitempty"`
}

// FileRetentionInventoryPlan is exact hash-covered inventory evidence.
type FileRetentionInventoryPlan struct {
	Identity             string `json:"identity"`
	RelativePath         string `json:"relative_path"`
	Device               string `json:"device"`
	Inode                string `json:"inode"`
	LinkCount            string `json:"link_count"`
	LogicalBytes         string `json:"logical_bytes"`
	AllocatedBytes       string `json:"allocated_bytes,omitempty"`
	AllocatedKnown       bool   `json:"allocated_known"`
	ModifiedAt           string `json:"modified_at"`
	GenerationCreatedAt  string `json:"generation_created_at"`
	GenerationProvenance string `json:"generation_provenance"`
	Generation           string `json:"generation"`
	ContentSHA256        string `json:"content_sha256"`
	Verified             bool   `json:"verified"`
	VerificationDigest   string `json:"verification_digest,omitempty"`
	VerificationReason   string `json:"verification_reason,omitempty"`
	MetadataRelativePath string `json:"metadata_relative_path,omitempty"`
	MetadataSHA256       string `json:"metadata_sha256,omitempty"`
	Pinned               bool   `json:"pinned"`
	Protected            bool   `json:"protected"`
	BlockingReason       string `json:"blocking_reason,omitempty"`
}

// FileRetentionCeilingPlan reports exact current/projected values.
type FileRetentionCeilingPlan struct {
	Ceiling   string `json:"ceiling"`
	Current   string `json:"current"`
	Projected string `json:"projected"`
}

// FileRetentionFloorPlan identifies the protected current-generation recovery point.
type FileRetentionFloorPlan struct {
	Identity           string `json:"identity"`
	RelativePath       string `json:"relative_path"`
	Generation         string `json:"generation"`
	ContentSHA256      string `json:"content_sha256"`
	VerificationDigest string `json:"verification_digest"`
}

// FileRetentionCandidatePlan is one exact ordered deletion intent.
type FileRetentionCandidatePlan struct {
	Identity     string   `json:"identity"`
	RelativePath string   `json:"relative_path"`
	Reasons      []string `json:"reasons"`
}

// FileRetentionBatchPlan enforces one durable batch per candidate.
type FileRetentionBatchPlan struct {
	Ordinal  string `json:"ordinal"`
	Identity string `json:"identity"`
}

// FileRetentionApplyResult reports idempotent execution outcomes.
type FileRetentionApplyResult struct {
	PlanID           string
	CandidateCount   int
	DeletedCount     int
	AlreadyCommitted int
	ConflictedCount  int
}
