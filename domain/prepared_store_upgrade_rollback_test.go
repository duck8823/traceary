package domain

import (
	"strings"
	"testing"
	"time"
)

func TestUpgradeRunRefusesCommittedToRollbackSwapIntent(t *testing.T) {
	t.Parallel()
	run := PreparedStoreUpgradeRun{
		Phase:     PreparedStoreUpgradeCommitted,
		Operation: PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
	}
	_, err := run.Advance(PreparedStoreUpgradeRollbackSwapIntent, time.Now())
	if err == nil {
		t.Fatal("upgrade run allowed committed → rollback_swap_intent")
	}
	if !strings.Contains(err.Error(), "forensic backup") {
		t.Fatalf("error = %v, want forensic-backup wording", err)
	}
}

func TestCompactionRunStillAllowsCommittedToRollbackSwapIntent(t *testing.T) {
	t.Parallel()
	run := PreparedStoreUpgradeRun{
		Phase:     PreparedStoreUpgradeCommitted,
		Operation: PreparedStoreUpgradeOperationCompaction,
	}
	got, err := run.Advance(PreparedStoreUpgradeRollbackSwapIntent, time.Now())
	if err != nil {
		t.Fatalf("compaction rollback transition: %v", err)
	}
	if got.Phase != PreparedStoreUpgradeRollbackSwapIntent {
		t.Fatalf("phase = %q", got.Phase)
	}
}
