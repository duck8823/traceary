package usecase

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestEncodeFileRetentionPlanReconstructsDecision(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	maxCount := 1
	classPlan, err := buildFileRetentionClassPlan(apptypes.FileRetentionInventorySnapshot{
		Class: "backup", Root: "/tmp/backups", RootIdentity: repeatedFileRetentionHex('a'), LiveGeneration: repeatedFileRetentionHex('b'),
		Entries: []apptypes.FileRetentionInventoryEntry{
			fileRetentionCodecEntry("old.db", repeatedFileRetentionHex('c'), repeatedFileRetentionHex('1'), repeatedFileRetentionHex('b'), now.Add(-2*time.Hour)),
			fileRetentionCodecEntry("new.db", repeatedFileRetentionHex('d'), repeatedFileRetentionHex('2'), repeatedFileRetentionHex('b'), now.Add(-time.Hour)),
		},
	}, apptypes.FileRetentionBudgetInput{MaxCount: &maxCount}, now)
	if err != nil {
		t.Fatalf("buildFileRetentionClassPlan() error = %v", err)
	}
	plan := apptypes.FileRetentionPlan{CanonicalPayload: apptypes.FileRetentionCanonicalPayload{
		SchemaVersion: fileRetentionPlanSchemaVersion, CreatedAt: now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), DatabasePath: "/tmp/live.db",
		Classes: []apptypes.FileRetentionClassPlan{classPlan},
	}}
	if _, err := encodeFileRetentionPlan(plan); err != nil {
		t.Fatalf("encodeFileRetentionPlan(valid) error = %v", err)
	}

	plan.CanonicalPayload.Classes[0].Candidates[0].Reasons = []string{"age"}
	if _, err := encodeFileRetentionPlan(plan); err == nil {
		t.Fatal("encodeFileRetentionPlan(rehashed decision tamper) error = nil")
	}
}

func fileRetentionCodecEntry(path, identity, digest, generation string, createdAt time.Time) apptypes.FileRetentionInventoryEntry {
	return apptypes.FileRetentionInventoryEntry{
		Identity: identity, RelativePath: path, Device: 1, Inode: 1, LinkCount: 1,
		LogicalBytes: 10, AllocatedBytes: 10, AllocatedKnown: true, ModifiedAt: createdAt,
		GenerationCreatedAt: createdAt, GenerationProvenance: "backup_manifest", Generation: generation,
		ContentSHA256: digest, Verified: true, VerificationDigest: repeatedFileRetentionHex('e'),
		MetadataRelativePath: apptypes.BackupRetentionManifestName(path), MetadataSHA256: repeatedFileRetentionHex('f'),
	}
}

func repeatedFileRetentionHex(value byte) string {
	result := make([]byte, 64)
	for index := range result {
		result[index] = value
	}
	return string(result)
}
